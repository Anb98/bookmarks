// ---------- responsive flags ----------

export const mobileQuery = window.matchMedia('(max-width: 1023px)');
export const wideQuery = window.matchMedia('(min-width: 1280px)');

export function isWide() {
  return wideQuery.matches;
}

// ---------- app state ----------

export const state = {
  q: '',
  activeTags: new Set(),
  bookmarks: [],
  tags: [],
  matched: 0,
  totalBookmarks: 0,
  totalTags: 0,
  editingId: null,
  draftTags: [],
  isMobile: mobileQuery.matches,
  tagBandOpen: !mobileQuery.matches,
};

export const dirty = { title: false, description: false };
