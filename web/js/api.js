export function listBookmarks(params, signal) {
  return fetch('/api/bookmarks?' + params.toString(), { signal: signal });
}

export function postMetadata(url, signal) {
  return fetch('/api/metadata', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url: url }),
    signal: signal,
  });
}

export function saveBookmark(endpoint, method, payload) {
  return fetch(endpoint, {
    method: method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export function deleteBookmark(id) {
  return fetch('/api/bookmarks/' + id, { method: 'DELETE' });
}

export function postImport(body) {
  return fetch('/api/import', { method: 'POST', body: body });
}
