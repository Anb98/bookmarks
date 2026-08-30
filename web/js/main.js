import { qs, safeGet, safeSet } from './dom.js';
import { state, dirty, mobileQuery, isWide } from './state.js';
import { postImport } from './api.js';
import { load, debouncedLoad } from './list.js';
import { renderTagBandChrome, renderTagCloud, renderGrid } from './render.js';
import {
  renderTokens,
  renderSuggestions,
  commitFirstSuggestionOrCreate,
  scrollTagClusterIntoView,
} from './tags.js';
import {
  closeDrawer,
  openDrawerForNew,
  handleSave,
  handleDelete,
  debouncedFetchMetadata,
  typingFetchMetadata,
} from './drawer.js';

// ---------- theme ----------

const THEME_KEY = 'bookmarks-theme';

function updateThemeToggleLabel() {
  const dark = document.documentElement.dataset.theme === 'dark';
  qs('theme-toggle').textContent = dark ? 'LIGHT' : 'DARK';
}

function initTheme() {
  const stored = safeGet(THEME_KEY);
  if (stored === 'light' || stored === 'dark') {
    document.documentElement.dataset.theme = stored;
  }
  updateThemeToggleLabel();
}

function toggleTheme() {
  const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
  document.documentElement.dataset.theme = next;
  safeSet(THEME_KEY, next);
  updateThemeToggleLabel();
}

// ---------- visual viewport (keyboard-aware sheet height) ----------

function updateVVH() {
  if (!window.visualViewport) return;
  document.documentElement.style.setProperty('--vvh', window.visualViewport.height + 'px');
}

// ---------- import / export ----------

function triggerExport() {
  const a = document.createElement('a');
  a.href = '/api/export';
  a.download = 'bookmarks-export.json';
  document.body.appendChild(a);
  a.click();
  a.remove();
}

function triggerImport(file) {
  file.text()
    .then(function (body) {
      return postImport(body);
    })
    .then(function (res) {
      return res.json().then(function (data) { return { ok: res.ok, data: data }; });
    })
    .then(function (result) {
      if (!result.ok) {
        window.alert('import failed');
        return;
      }
      const data = result.data;
      let summary = 'created ' + data.created + ', updated ' + data.updated + ', skipped ' + data.skipped;
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
  // Crossing the breakpoint changes what the band default should be: open on
  // desktop, collapsed where a wrapping cloud would eat the viewport. Without
  // this the band keeps whichever state it was built with, so a window resized
  // down keeps a full-height cloud and one resized up keeps a collapsed strip.
  mobileQuery.addEventListener('change', function (e) {
    state.isMobile = e.matches;
    state.tagBandOpen = !state.isMobile;
    renderTagBandChrome();
    renderTagCloud();
    renderGrid();
  });

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
    state.tagBandOpen = false;
    renderTagBandChrome();
  });
  qs('tag-band-collapsed').addEventListener('click', function () {
    state.tagBandOpen = true;
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
    const v = e.target.value.trim();
    if (v.length >= 4 && /[^\s/?#:]\.[^\s/?#:.]{2,}/.test(v)) {
      typingFetchMetadata(v);
    }
  });
  qs('field-title').addEventListener('input', function () { dirty.title = true; });
  qs('field-description').addEventListener('input', function () { dirty.description = true; });

  const tagInput = qs('field-tag-input');
  tagInput.addEventListener('input', renderSuggestions);
  tagInput.addEventListener('focus', scrollTagClusterIntoView);
  tagInput.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      commitFirstSuggestionOrCreate();
    } else if (e.key === 'Backspace' && !tagInput.value && state.draftTags.length) {
      state.draftTags.pop();
      renderTokens();
      renderSuggestions();
    }
  });

  qs('export-btn').addEventListener('click', triggerExport);
  qs('import-btn').addEventListener('click', function () { qs('import-input').click(); });
  qs('import-input').addEventListener('change', function () {
    const file = qs('import-input').files[0];
    qs('import-input').value = '';
    if (file) triggerImport(file);
  });

  load();
}

init();
