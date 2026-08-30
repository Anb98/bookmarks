import { qs } from './dom.js';
import { state } from './state.js';
import { toggleTag } from './list.js';
import { openDrawerForEdit } from './drawer.js';

// ---------- rendering ----------

export function renderAll() {
  qs('stats').textContent = state.totalBookmarks + ' ENTRIES / ' + state.totalTags + ' TAGS';
  qs('result-count').textContent = state.matched + ' RESULT' + (state.matched === 1 ? '' : 'S');
  renderTagBandChrome();
  renderTagCloud();
  renderGrid();
}

export function renderTagBandChrome() {
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
  qs('tag-band-open').hidden = !state.tagBandOpen;
  qs('tag-band-collapsed').hidden = state.tagBandOpen;
  qs('tag-band-collapsed').setAttribute('aria-expanded', String(state.tagBandOpen));
}

export function renderTagCloud() {
  const cloud = qs('tag-cloud');
  cloud.textContent = '';
  if (!state.tags.length) return;

  const counts = state.tags.map(function (t) { return t.count; });
  const lo = Math.min.apply(null, counts);
  const hi = Math.max.apply(null, counts);

  state.tags.forEach(function (t) {
    const active = state.activeTags.has(t.name);
    const r = hi === lo ? 1 : (t.count - lo) / (hi - lo);
    const px = 11 + Math.round(r * 4);

    const btn = document.createElement('button');
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
    const label = t.name + ', ' + t.count + ' bookmarks';
    btn.setAttribute('aria-label', label);
    btn.title = label;
    btn.addEventListener('click', function () { toggleTag(t.name); });
    cloud.appendChild(btn);
  });
}

function makeMonogram(domain) {
  const div = document.createElement('div');
  div.className = 'card-monogram';
  div.setAttribute('aria-hidden', 'true');
  div.textContent = (domain || '?').charAt(0).toUpperCase();
  return div;
}

function makeEditButton(bm) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'card-edit';
  btn.setAttribute('aria-label', 'edit ' + (bm.title || bm.url));
  btn.title = 'edit';
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 14 14');
  svg.setAttribute('aria-hidden', 'true');
  const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
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
  const root = document.createElement('div');
  root.className = 'card';
  root.dataset.id = String(bm.id);

  function makeIcon() {
    if (!bm.has_icon) return makeMonogram(bm.domain);
    const img = document.createElement('img');
    img.className = 'card-favicon';
    img.src = '/api/favicon/' + bm.domain;
    img.alt = '';
    img.addEventListener('error', function () {
      img.replaceWith(makeMonogram(bm.domain));
    }, { once: true });
    return img;
  }

  function makeDomain() {
    const d = document.createElement('span');
    d.className = 'card-domain';
    d.textContent = bm.domain;
    return d;
  }

  function makeTitle() {
    const t = document.createElement('span');
    t.className = 'card-title';
    t.textContent = bm.title || bm.url;
    return t;
  }

  // The card is a real anchor at every width, so middle-click, ctrl-click and
  // "open in new tab" all work. Interactive controls (tag chips, edit) are
  // siblings of the link, never descendants: a button inside an anchor is
  // invalid and swallows the tap.
  const link = document.createElement('a');
  link.className = 'card-link';
  link.href = bm.url;
  link.target = '_blank';
  link.rel = 'noreferrer';

  const tagsWrap = document.createElement('div');
  tagsWrap.className = 'card-tags';
  (bm.tags || []).forEach(function (t) {
    const chip = document.createElement(state.isMobile ? 'span' : 'button');
    if (!state.isMobile) chip.type = 'button';
    chip.className = 'card-tag-chip' + (state.activeTags.has(t) ? ' active' : '');
    chip.textContent = t;
    if (!state.isMobile) {
      chip.addEventListener('click', function (e) {
        e.preventDefault();
        e.stopPropagation();
        toggleTag(t);
      });
    }
    tagsWrap.appendChild(chip);
  });

  if (state.isMobile) {
    root.classList.add('card-row');
    link.appendChild(makeIcon());
    const main = document.createElement('div');
    main.className = 'card-row-main';
    main.appendChild(makeTitle());
    main.appendChild(makeDomain());
    main.appendChild(tagsWrap);
    link.appendChild(main);
    root.appendChild(link);
  } else {
    const head = document.createElement('div');
    head.className = 'card-head';
    head.appendChild(makeIcon());
    head.appendChild(makeDomain());
    link.appendChild(head);
    link.appendChild(makeTitle());
    const desc = document.createElement('p');
    desc.className = 'card-description';
    desc.textContent = bm.description;
    link.appendChild(desc);
    root.appendChild(link);
    root.appendChild(tagsWrap);
  }

  root.appendChild(makeEditButton(bm));
  return root;
}

export function renderGrid() {
  const grid = qs('grid');
  const emptyState = qs('empty-state');
  grid.textContent = '';

  if (!state.bookmarks.length) {
    grid.hidden = true;
    emptyState.hidden = false;
    qs('empty-body').textContent = state.isMobile
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
