import { qs, debounce } from './dom.js';
import { state, dirty, isWide } from './state.js';
import { postMetadata, saveBookmark, deleteBookmark } from './api.js';
import { renderTokens, renderSuggestions } from './tags.js';
import { load, revealBookmark } from './list.js';

// ---------- drawer ----------

let savedScrollY = 0;

export function showDrawer() {
  qs('content').classList.add('drawer-open');
  qs('drawer').hidden = false;
  qs('scrim').hidden = isWide();
  savedScrollY = window.scrollY;
  document.body.classList.add('no-scroll');
  qs('field-url').focus();
}

export function closeDrawer() {
  qs('drawer').hidden = true;
  qs('scrim').hidden = true;
  qs('content').classList.remove('drawer-open');
  document.body.classList.remove('no-scroll');
  window.scrollTo(0, savedScrollY);
}

function hideMetadataNotice() {
  const el = qs('metadata-notice');
  el.hidden = true;
  el.textContent = '';
}

export function hideSaveError() {
  const el = qs('save-error');
  el.hidden = true;
  el.textContent = '';
}

export function setSaveError(text) {
  const el = qs('save-error');
  el.hidden = false;
  el.textContent = text;
}

export function openDrawerForNew() {
  state.editingId = null;
  qs('delete-btn').hidden = true;
  dirty.title = false;
  dirty.description = false;
  state.draftTags = [];
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

export function openDrawerForEdit(bm) {
  state.editingId = bm.id;
  qs('delete-btn').hidden = false;
  dirty.title = !!bm.title;
  dirty.description = !!bm.description;
  state.draftTags = bm.tags ? bm.tags.slice() : [];
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

let metaAbort = null;
let metaRequestId = 0;

function setMetadataNotice(text) {
  const el = qs('metadata-notice');
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
  const myId = ++metaRequestId;
  const trimmed = rawUrl.trim();
  if (!trimmed) {
    setMetadataNotice('');
    return;
  }

  const controller = new AbortController();
  metaAbort = controller;
  setMetadataNotice('Fetching page details…');

  postMetadata(trimmed, controller.signal)
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

export const debouncedFetchMetadata = debounce(fetchMetadataFor, 300);
export const typingFetchMetadata = debounce(fetchMetadataFor, 800);

export function handleDelete() {
  const id = state.editingId;
  if (!id) return;
  const label = qs('field-title').value.trim() || qs('field-url').value.trim();
  if (!window.confirm('Delete "' + label + '"? This cannot be undone.')) return;
  hideSaveError();
  deleteBookmark(id)
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

export function handleSave() {
  const url = qs('field-url').value.trim();
  if (!url) {
    setSaveError('a url is required');
    return;
  }
  hideSaveError();

  const payload = {
    url: url,
    title: qs('field-title').value.trim(),
    description: qs('field-description').value.trim(),
    tags: state.draftTags.slice(),
  };
  const editingId = state.editingId;
  const endpoint = editingId ? '/api/bookmarks/' + editingId : '/api/bookmarks';
  const method = editingId ? 'PUT' : 'POST';

  saveBookmark(endpoint, method, payload)
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
