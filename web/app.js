(function () {
  'use strict';

  function qs(id) {
    return document.getElementById(id);
  }

  function debounce(fn, ms) {
    var timer = null;
    return function () {
      var args = arguments;
      clearTimeout(timer);
      timer = setTimeout(function () {
        fn.apply(null, args);
      }, ms);
    };
  }

  function safeGet(key) {
    try {
      return localStorage.getItem(key);
    } catch (e) {
      return null;
    }
  }

  function safeSet(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch (e) {
      /* private mode or storage disabled: theme choice just won't persist */
    }
  }

  function normalizeTagClient(s) {
    return s.trim().toLowerCase().replace(/\s+/g, ' ').slice(0, 32);
  }

  // ---------- theme ----------

  var THEME_KEY = 'bookmarks-theme';

  function updateThemeToggleLabel() {
    var dark = document.documentElement.dataset.theme === 'dark';
    qs('theme-toggle').textContent = dark ? 'LIGHT' : 'DARK';
  }

  function initTheme() {
    var stored = safeGet(THEME_KEY);
    if (stored === 'light' || stored === 'dark') {
      document.documentElement.dataset.theme = stored;
    }
    updateThemeToggleLabel();
  }

  function toggleTheme() {
    var next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    document.documentElement.dataset.theme = next;
    safeSet(THEME_KEY, next);
    updateThemeToggleLabel();
  }

  // ---------- visual viewport (keyboard-aware sheet height) ----------

  function updateVVH() {
    if (!window.visualViewport) return;
    document.documentElement.style.setProperty('--vvh', window.visualViewport.height + 'px');
  }

  // ---------- responsive flags ----------

  var mobileQuery = window.matchMedia('(max-width: 1023px)');
  var wideQuery = window.matchMedia('(min-width: 1280px)');
  var isMobile = mobileQuery.matches;
  var tagBandOpen = !isMobile;

  function isWide() {
    return wideQuery.matches;
  }

  // ---------- app state ----------

  var state = {
    q: '',
    activeTags: new Set(),
    bookmarks: [],
    tags: [],
    matched: 0,
    totalBookmarks: 0,
    totalTags: 0,
    editingId: null,
  };

  var draftTags = [];
  var dirty = { title: false, description: false };

  // ---------- list loading ----------

  var loadAbort = null;

  function load() {
    if (loadAbort) loadAbort.abort();
    var controller = new AbortController();
    loadAbort = controller;

    var params = new URLSearchParams();
    if (state.q) params.set('q', state.q);
    if (state.activeTags.size) params.set('tags', Array.from(state.activeTags).join(','));

    return fetch('/api/bookmarks?' + params.toString(), { signal: controller.signal })
      .then(function (res) {
        if (controller.signal.aborted) return null;
        if (!res.ok) throw new Error('list request failed');
        return res.json();
      })
      .then(function (data) {
        if (!data || controller.signal.aborted) return;
        state.bookmarks = data.bookmarks || [];
        state.tags = data.tags || [];
        state.matched = data.matched || 0;
        state.totalBookmarks = data.total_bookmarks || 0;
        state.totalTags = data.total_tags || 0;
        renderAll();
      })
      .catch(function (err) {
        if (err.name === 'AbortError') return;
      });
  }

  var debouncedLoad = debounce(load, 120);

  function toggleTag(name) {
    if (state.activeTags.has(name)) {
      state.activeTags.delete(name);
    } else {
      state.activeTags.add(name);
    }
    load();
  }

  // ---------- rendering ----------

  function renderAll() {
    qs('stats').textContent = state.totalBookmarks + ' ENTRIES / ' + state.totalTags + ' TAGS';
    qs('result-count').textContent = state.matched + ' RESULT' + (state.matched === 1 ? '' : 'S');
    renderTagBandChrome();
    renderTagCloud();
    renderGrid();
  }

  function renderTagBandChrome() {
    // The size/brightness ramp encodes how often a tag is used, which is not
    // self-evident on its own. A legend explains it without printing a count
    // per tag, which the design rules out; the exact number stays in each
    // pill's tooltip and aria-label.
    qs('tag-band-label').textContent = state.activeTags.size
      ? state.activeTags.size + ' TAG FILTERS ACTIVE'
      : 'ALL ' + state.totalTags + ' TAGS \u00b7 SIZED BY USE';
    qs('tag-clear').hidden = state.activeTags.size === 0;
    qs('tag-band-summary').textContent = state.activeTags.size
      ? Array.from(state.activeTags).join(' + ')
      : state.totalTags + ' tags, none active';
    qs('tag-band-open').hidden = !tagBandOpen;
    qs('tag-band-collapsed').hidden = tagBandOpen;
    qs('tag-band-collapsed').setAttribute('aria-expanded', String(tagBandOpen));
  }

  function renderTagCloud() {
    var cloud = qs('tag-cloud');
    cloud.textContent = '';
    if (!state.tags.length) return;

    var counts = state.tags.map(function (t) { return t.count; });
    var lo = Math.min.apply(null, counts);
    var hi = Math.max.apply(null, counts);

    state.tags.forEach(function (t) {
      var active = state.activeTags.has(t.name);
      var r = hi === lo ? 1 : (t.count - lo) / (hi - lo);
      var px = 11 + Math.round(r * 4);

      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'tag-pill sz-' + px;
      if (active) {
        btn.classList.add('active');
      } else if (r > 0.55) {
        btn.classList.add('freq-hi');
      } else if (r > 0.2) {
        btn.classList.add('freq-mid');
      } else {
        btn.classList.add('freq-low');
      }
      btn.textContent = t.name;
      btn.setAttribute('aria-pressed', String(active));
      var label = t.name + ', ' + t.count + ' bookmarks';
      btn.setAttribute('aria-label', label);
      btn.title = label;
      btn.addEventListener('click', function () { toggleTag(t.name); });
      cloud.appendChild(btn);
    });
  }

  function makeMonogram(domain) {
    var div = document.createElement('div');
    div.className = 'card-monogram';
    div.setAttribute('aria-hidden', 'true');
    div.textContent = (domain || '?').charAt(0).toUpperCase();
    return div;
  }

  function makeEditButton(bm) {
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'card-edit';
    btn.setAttribute('aria-label', 'edit ' + (bm.title || bm.url));
    btn.title = 'edit';
    var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 14 14');
    svg.setAttribute('aria-hidden', 'true');
    var path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', 'M9.5 1.8 L12.2 4.5 L4.7 12 L2 12 L2 9.3 Z');
    svg.appendChild(path);
    btn.appendChild(svg);
    btn.addEventListener('click', function (e) {
      e.preventDefault();
      e.stopPropagation();
      openDrawerForEdit(bm);
    });
    return btn;
  }

  function createCard(bm) {
    var root = document.createElement('div');
    root.className = 'card';
    root.dataset.id = String(bm.id);

    function makeIcon() {
      if (!bm.has_icon) return makeMonogram(bm.domain);
      var img = document.createElement('img');
      img.className = 'card-favicon';
      img.src = '/api/favicon/' + bm.domain;
      img.alt = '';
      img.addEventListener('error', function () {
        img.replaceWith(makeMonogram(bm.domain));
      }, { once: true });
      return img;
    }

    function makeDomain() {
      var d = document.createElement('span');
      d.className = 'card-domain';
      d.textContent = bm.domain;
      return d;
    }

    function makeTitle() {
      var t = document.createElement('span');
      t.className = 'card-title';
      t.textContent = bm.title || bm.url;
      return t;
    }

    // The card is a real anchor at every width, so middle-click, ctrl-click and
    // "open in new tab" all work. Interactive controls (tag chips, edit) are
    // siblings of the link, never descendants: a button inside an anchor is
    // invalid and swallows the tap.
    var link = document.createElement('a');
    link.className = 'card-link';
    link.href = bm.url;
    link.target = '_blank';
    link.rel = 'noreferrer';

    var tagsWrap = document.createElement('div');
    tagsWrap.className = 'card-tags';
    (bm.tags || []).forEach(function (t) {
      var chip = document.createElement(isMobile ? 'span' : 'button');
      if (!isMobile) chip.type = 'button';
      chip.className = 'card-tag-chip' + (state.activeTags.has(t) ? ' active' : '');
      chip.textContent = t;
      if (!isMobile) {
        chip.addEventListener('click', function (e) {
          e.preventDefault();
          e.stopPropagation();
          toggleTag(t);
        });
      }
      tagsWrap.appendChild(chip);
    });

    if (isMobile) {
      root.classList.add('card-row');
      link.appendChild(makeIcon());
      var main = document.createElement('div');
      main.className = 'card-row-main';
      main.appendChild(makeTitle());
      main.appendChild(makeDomain());
      main.appendChild(tagsWrap);
      link.appendChild(main);
      root.appendChild(link);
    } else {
      var head = document.createElement('div');
      head.className = 'card-head';
      head.appendChild(makeIcon());
      head.appendChild(makeDomain());
      link.appendChild(head);
      link.appendChild(makeTitle());
      var desc = document.createElement('p');
      desc.className = 'card-description';
      desc.textContent = bm.description;
      link.appendChild(desc);
      root.appendChild(link);
      root.appendChild(tagsWrap);
    }

    root.appendChild(makeEditButton(bm));
    return root;
  }

  function renderGrid() {
    var grid = qs('grid');
    var emptyState = qs('empty-state');
    grid.textContent = '';

    if (!state.bookmarks.length) {
      grid.hidden = true;
      emptyState.hidden = false;
      qs('empty-body').textContent = isMobile
        ? 'No entry matches that search and tag.'
        : 'No entry matches every active tag and that query.';
      return;
    }

    grid.hidden = false;
    emptyState.hidden = true;
    state.bookmarks.forEach(function (bm) {
      grid.appendChild(createCard(bm));
    });
  }

  // Crossing the breakpoint changes what the band default should be: open on
  // desktop, collapsed where a wrapping cloud would eat the viewport. Without
  // this the band keeps whichever state it was built with, so a window resized
  // down keeps a full-height cloud and one resized up keeps a collapsed strip.
  mobileQuery.addEventListener('change', function (e) {
    isMobile = e.matches;
    tagBandOpen = !isMobile;
    renderTagBandChrome();
    renderTagCloud();
    renderGrid();
  });

  // ---------- drawer ----------

  var savedScrollY = 0;

  function showDrawer() {
    qs('content').classList.add('drawer-open');
    qs('drawer').hidden = false;
    qs('scrim').hidden = isWide();
    savedScrollY = window.scrollY;
    document.body.classList.add('no-scroll');
    qs('field-url').focus();
  }

  function closeDrawer() {
    qs('drawer').hidden = true;
    qs('scrim').hidden = true;
    qs('content').classList.remove('drawer-open');
    document.body.classList.remove('no-scroll');
    window.scrollTo(0, savedScrollY);
  }

  function hideMetadataNotice() {
    var el = qs('metadata-notice');
    el.hidden = true;
    el.textContent = '';
  }

  function hideSaveError() {
    var el = qs('save-error');
    el.hidden = true;
    el.textContent = '';
  }

  function setSaveError(text) {
    var el = qs('save-error');
    el.hidden = false;
    el.textContent = text;
  }

  function openDrawerForNew() {
    state.editingId = null;
    qs('delete-btn').hidden = true;
    dirty.title = false;
    dirty.description = false;
    draftTags = [];
    qs('field-url').value = '';
    qs('field-title').value = '';
    qs('field-description').value = '';
    qs('field-tag-input').value = '';
    qs('drawer-title').textContent = 'NEW ENTRY';
    hideMetadataNotice();
    hideSaveError();
    renderTokens();
    renderSuggestions();
    showDrawer();
  }

  function openDrawerForEdit(bm) {
    state.editingId = bm.id;
    qs('delete-btn').hidden = false;
    dirty.title = !!bm.title;
    dirty.description = !!bm.description;
    draftTags = bm.tags ? bm.tags.slice() : [];
    qs('field-url').value = bm.url;
    qs('field-title').value = bm.title;
    qs('field-description').value = bm.description;
    qs('field-tag-input').value = '';
    qs('drawer-title').textContent = 'EDIT ENTRY';
    hideMetadataNotice();
    hideSaveError();
    renderTokens();
    renderSuggestions();
    showDrawer();
  }

  // ---------- metadata prefetch ----------

  var metaAbort = null;
  var metaRequestId = 0;

  function setMetadataNotice(text) {
    var el = qs('metadata-notice');
    if (!text) {
      el.hidden = true;
      el.textContent = '';
      return;
    }
    el.hidden = false;
    el.textContent = text;
  }

  function fetchMetadataFor(rawUrl) {
    if (metaAbort) {
      metaAbort.abort();
      metaAbort = null;
    }
    var myId = ++metaRequestId;
    var trimmed = rawUrl.trim();
    if (!trimmed) {
      setMetadataNotice('');
      return;
    }

    var controller = new AbortController();
    metaAbort = controller;
    setMetadataNotice('Fetching page details…');

    fetch('/api/metadata', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url: trimmed }),
      signal: controller.signal,
    })
      .then(function (res) {
        if (!res.ok) throw new Error('metadata request failed');
        return res.json();
      })
      .then(function (data) {
        if (myId !== metaRequestId) return;
        if (data.fetch_error) {
          setMetadataNotice('Could not fetch page details. Fill in manually.');
          return;
        }
        if (!dirty.title && data.title) qs('field-title').value = data.title;
        if (!dirty.description && data.description) qs('field-description').value = data.description;
        setMetadataNotice('');
      })
      .catch(function () {
        if (myId !== metaRequestId) return;
        setMetadataNotice('Could not fetch page details. Fill in manually.');
      })
      .finally(function () {
        if (myId === metaRequestId) metaAbort = null;
      });
  }

  var debouncedFetchMetadata = debounce(fetchMetadataFor, 300);
  var typingFetchMetadata = debounce(fetchMetadataFor, 800);

  // ---------- tag token input ----------

  function computeCandidates(query) {
    var available = state.tags.filter(function (t) { return draftTags.indexOf(t.name) === -1; });
    var candidates = query
      ? available.filter(function (t) { return t.name.indexOf(query) !== -1; })
      : available.slice();
    candidates.sort(function (a, b) { return b.count - a.count; });
    return candidates.slice(0, 8);
  }

  function existsAnywhere(query) {
    return draftTags.indexOf(query) !== -1 || state.tags.some(function (t) { return t.name === query; });
  }

  function renderTokens() {
    var box = qs('token-box');
    var input = qs('field-tag-input');
    box.textContent = '';
    draftTags.forEach(function (tag) {
      var chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'token-chip';
      var label = document.createElement('span');
      label.textContent = tag;
      chip.appendChild(label);
      var svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.setAttribute('viewBox', '0 0 8 8');
      svg.setAttribute('width', '8');
      svg.setAttribute('height', '8');
      svg.setAttribute('aria-hidden', 'true');
      var path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      path.setAttribute('d', 'M1 1 L7 7 M7 1 L1 7');
      svg.appendChild(path);
      chip.appendChild(svg);
      chip.addEventListener('click', function () {
        draftTags = draftTags.filter(function (t) { return t !== tag; });
        renderTokens();
        renderSuggestions();
      });
      box.appendChild(chip);
    });
    box.appendChild(input);
    input.placeholder = draftTags.length ? 'add another' : 'type to search or create';
  }

  function commitTag(name) {
    var norm = normalizeTagClient(name);
    if (!norm || draftTags.indexOf(norm) !== -1) return;
    draftTags.push(norm);
    qs('field-tag-input').value = '';
    renderTokens();
    renderSuggestions();
  }

  function renderSuggestions() {
    var input = qs('field-tag-input');
    var query = normalizeTagClient(input.value);
    var list = qs('suggestions-list');
    qs('suggestions-label').textContent = query ? 'MATCHING TAGS' : 'MOST USED';
    list.textContent = '';

    var candidates = computeCandidates(query);
    var canCreate = !!query && !existsAnywhere(query);

    if (!candidates.length && !canCreate) {
      var msg = document.createElement('p');
      msg.className = 'no-suggestions';
      msg.textContent = 'every tag is already on this bookmark';
      list.appendChild(msg);
      return;
    }

    candidates.forEach(function (t) {
      var chip = document.createElement('button');
      chip.type = 'button';
      chip.className = 'suggestion-chip';
      var label = document.createElement('span');
      label.textContent = t.name;
      chip.appendChild(label);
      var count = document.createElement('span');
      count.className = 'suggestion-count';
      count.textContent = String(t.count);
      chip.appendChild(count);
      chip.addEventListener('click', function () { commitTag(t.name); });
      list.appendChild(chip);
    });

    if (canCreate) {
      var create = document.createElement('button');
      create.type = 'button';
      create.className = 'create-chip';
      create.textContent = '+ create ' + query;
      create.addEventListener('click', function () { commitTag(query); });
      list.appendChild(create);
    }
  }

  function commitFirstSuggestionOrCreate() {
    var query = normalizeTagClient(qs('field-tag-input').value);
    if (!query) return;
    var candidates = computeCandidates(query);
    commitTag(candidates.length ? candidates[0].name : query);
  }

  function scrollTagClusterIntoView() {
    qs('tag-cluster').scrollIntoView({ block: 'nearest', behavior: 'smooth' });
  }

  // ---------- save / reveal ----------

  function revealBookmark(id) {
    state.q = '';
    qs('search-input').value = '';
    state.activeTags.clear();
    load().then(function () {
      var card = document.querySelector('[data-id="' + id + '"]');
      if (!card) return;
      card.scrollIntoView({ block: 'center' });
      card.classList.add('flash');
      setTimeout(function () { card.classList.remove('flash'); }, 1500);
    });
  }

  function handleDelete() {
    var id = state.editingId;
    if (!id) return;
    var label = qs('field-title').value.trim() || qs('field-url').value.trim();
    if (!window.confirm('Delete "' + label + '"? This cannot be undone.')) return;
    hideSaveError();
    fetch('/api/bookmarks/' + id, { method: 'DELETE' })
      .then(function (res) {
        if (!res.ok && res.status !== 204) {
          setSaveError('could not delete, try again');
          return;
        }
        closeDrawer();
        load();
      })
      .catch(function () {
        setSaveError('could not delete, try again');
      });
  }

  function handleSave() {
    var url = qs('field-url').value.trim();
    if (!url) {
      setSaveError('a url is required');
      return;
    }
    hideSaveError();

    var payload = {
      url: url,
      title: qs('field-title').value.trim(),
      description: qs('field-description').value.trim(),
      tags: draftTags.slice(),
    };
    var editingId = state.editingId;
    var endpoint = editingId ? '/api/bookmarks/' + editingId : '/api/bookmarks';
    var method = editingId ? 'PUT' : 'POST';

    fetch(endpoint, {
      method: method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    })
      .then(function (res) {
        if (res.status === 409) {
          return res.json().catch(function () { return {}; }).then(function (data) {
            setSaveError('already saved');
            if (data.id) revealBookmark(data.id);
          });
        }
        if (!res.ok) {
          setSaveError('could not save, try again');
          return;
        }
        closeDrawer();
        load();
      })
      .catch(function () {
        setSaveError('could not reach the server, try again');
      });
  }

  // ---------- import / export ----------

  function triggerExport() {
    var a = document.createElement('a');
    a.href = '/api/export';
    a.download = 'bookmarks-export.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
  }

  function triggerImport(file) {
    file.text()
      .then(function (body) {
        return fetch('/api/import', { method: 'POST', body: body });
      })
      .then(function (res) {
        return res.json().then(function (data) { return { ok: res.ok, data: data }; });
      })
      .then(function (result) {
        if (!result.ok) {
          window.alert('import failed');
          return;
        }
        var data = result.data;
        var summary = 'created ' + data.created + ', updated ' + data.updated + ', skipped ' + data.skipped;
        if (data.errors && data.errors.length) summary += ', ' + data.errors.length + ' error(s)';
        window.alert(summary);
        load();
      })
      .catch(function () {
        window.alert('import failed');
      });
  }

  // ---------- wiring ----------

  function init() {
    initTheme();
    updateVVH();
    renderTagBandChrome();

    if (window.visualViewport) {
      window.visualViewport.addEventListener('resize', updateVVH);
      window.visualViewport.addEventListener('scroll', updateVVH);
    }

    qs('search-input').addEventListener('input', function (e) {
      state.q = e.target.value;
      debouncedLoad();
    });

    qs('tag-hide').addEventListener('click', function () {
      tagBandOpen = false;
      renderTagBandChrome();
    });
    qs('tag-band-collapsed').addEventListener('click', function () {
      tagBandOpen = true;
      renderTagBandChrome();
    });
    qs('tag-clear').addEventListener('click', function () {
      state.activeTags.clear();
      load();
    });

    qs('new-btn').addEventListener('click', openDrawerForNew);
    qs('drawer-close').addEventListener('click', closeDrawer);
    qs('scrim').addEventListener('click', closeDrawer);
    qs('save-btn').addEventListener('click', handleSave);
    qs('delete-btn').addEventListener('click', handleDelete);
    qs('theme-toggle').addEventListener('click', toggleTheme);

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && !qs('drawer').hidden) closeDrawer();
    });

    window.addEventListener('resize', function () {
      if (!qs('drawer').hidden) qs('scrim').hidden = isWide();
    });

    qs('field-url').addEventListener('blur', function (e) {
      debouncedFetchMetadata(e.target.value);
    });
    qs('field-url').addEventListener('paste', function () {
      setTimeout(function () { debouncedFetchMetadata(qs('field-url').value); }, 0);
    });
    // Blur and paste alone are not enough: typing a url and simply waiting is
    // a normal way to finish, and it inferred nothing. Fires only once the
    // value plausibly contains a host, on a longer debounce than blur so it
    // waits for the typist to stop rather than firing per keystroke.
    qs('field-url').addEventListener('input', function (e) {
      var v = e.target.value.trim();
      if (v.length >= 4 && /[^\s/?#:]\.[^\s/?#:.]{2,}/.test(v)) {
        typingFetchMetadata(v);
      }
    });
    qs('field-title').addEventListener('input', function () { dirty.title = true; });
    qs('field-description').addEventListener('input', function () { dirty.description = true; });

    var tagInput = qs('field-tag-input');
    tagInput.addEventListener('input', renderSuggestions);
    tagInput.addEventListener('focus', scrollTagClusterIntoView);
    tagInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault();
        commitFirstSuggestionOrCreate();
      } else if (e.key === 'Backspace' && !tagInput.value && draftTags.length) {
        draftTags.pop();
        renderTokens();
        renderSuggestions();
      }
    });

    qs('export-btn').addEventListener('click', triggerExport);
    qs('import-btn').addEventListener('click', function () { qs('import-input').click(); });
    qs('import-input').addEventListener('change', function () {
      var file = qs('import-input').files[0];
      qs('import-input').value = '';
      if (file) triggerImport(file);
    });

    load();
  }

  init();
})();
