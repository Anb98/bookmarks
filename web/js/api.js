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

// Fire-and-forget: the browser is opening the bookmark in another tab at the
// same moment, so sendBeacon is the only send that is guaranteed to survive
// that. fetch() is the fallback for browsers without it; neither is awaited
// and a failure costs one uncounted visit.
export function recordVisit(id) {
  const path = '/api/bookmarks/' + id + '/visit';
  if (navigator.sendBeacon && navigator.sendBeacon(path)) return;
  fetch(path, { method: 'POST', keepalive: true }).catch(function () {});
}
