import { qs, debounce } from './dom.js';
import { state } from './state.js';
import { listBookmarks } from './api.js';
import { renderAll } from './render.js';

// ---------- list loading ----------

let loadAbort = null;

export function load() {
  if (loadAbort) loadAbort.abort();
  const controller = new AbortController();
  loadAbort = controller;

  const params = new URLSearchParams();
  if (state.q) params.set('q', state.q);
  if (state.activeTags.size) params.set('tags', Array.from(state.activeTags).join(','));
  if (state.sort !== 'recent') params.set('sort', state.sort);

  return listBookmarks(params, controller.signal)
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

export const debouncedLoad = debounce(load, 120);

export function toggleTag(name) {
  if (state.activeTags.has(name)) {
    state.activeTags.delete(name);
  } else {
    state.activeTags.add(name);
  }
  load();
}

// ---------- save / reveal ----------

export function revealBookmark(id) {
  state.q = '';
  qs('search-input').value = '';
  state.activeTags.clear();
  load().then(function () {
    const card = document.querySelector('[data-id="' + id + '"]');
    if (!card) return;
    card.scrollIntoView({ block: 'center' });
    card.classList.add('flash');
    setTimeout(function () { card.classList.remove('flash'); }, 1500);
  });
}
