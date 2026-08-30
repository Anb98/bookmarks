export function qs(id) {
  return document.getElementById(id);
}

export function debounce(fn, ms) {
  let timer = null;
  return function () {
    const args = arguments;
    clearTimeout(timer);
    timer = setTimeout(function () {
      fn.apply(null, args);
    }, ms);
  };
}

export function safeGet(key) {
  try {
    return localStorage.getItem(key);
  } catch (e) {
    return null;
  }
}

export function safeSet(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch (e) {
    /* private mode or storage disabled: theme choice just won't persist */
  }
}

export function normalizeTagClient(s) {
  return s.trim().toLowerCase().replace(/\s+/g, ' ').slice(0, 32);
}
