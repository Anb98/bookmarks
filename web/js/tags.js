import { qs, normalizeTagClient } from './dom.js';
import { state } from './state.js';

// ---------- tag token input ----------

function computeCandidates(query) {
  const available = state.tags.filter(function (t) { return state.draftTags.indexOf(t.name) === -1; });
  const candidates = query
    ? available.filter(function (t) { return t.name.indexOf(query) !== -1; })
    : available.slice();
  candidates.sort(function (a, b) { return b.count - a.count; });
  return candidates.slice(0, 8);
}

function existsAnywhere(query) {
  return state.draftTags.indexOf(query) !== -1 || state.tags.some(function (t) { return t.name === query; });
}

export function renderTokens() {
  const box = qs('token-box');
  const input = qs('field-tag-input');
  box.textContent = '';
  state.draftTags.forEach(function (tag) {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'token-chip';
    const label = document.createElement('span');
    label.textContent = tag;
    chip.appendChild(label);
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('viewBox', '0 0 8 8');
    svg.setAttribute('width', '8');
    svg.setAttribute('height', '8');
    svg.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', 'M1 1 L7 7 M7 1 L1 7');
    svg.appendChild(path);
    chip.appendChild(svg);
    chip.addEventListener('click', function () {
      state.draftTags = state.draftTags.filter(function (t) { return t !== tag; });
      renderTokens();
      renderSuggestions();
    });
    box.appendChild(chip);
  });
  box.appendChild(input);
  input.placeholder = state.draftTags.length ? 'add another' : 'type to search or create';
}

export function commitTag(name) {
  const norm = normalizeTagClient(name);
  if (!norm || state.draftTags.indexOf(norm) !== -1) return;
  state.draftTags.push(norm);
  qs('field-tag-input').value = '';
  renderTokens();
  renderSuggestions();
}

export function renderSuggestions() {
  const input = qs('field-tag-input');
  const query = normalizeTagClient(input.value);
  const list = qs('suggestions-list');
  qs('suggestions-label').textContent = query ? 'MATCHING TAGS' : 'MOST USED';
  list.textContent = '';

  const candidates = computeCandidates(query);
  const canCreate = !!query && !existsAnywhere(query);

  if (!candidates.length && !canCreate) {
    const msg = document.createElement('p');
    msg.className = 'no-suggestions';
    msg.textContent = 'every tag is already on this bookmark';
    list.appendChild(msg);
    return;
  }

  candidates.forEach(function (t) {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'suggestion-chip';
    const label = document.createElement('span');
    label.textContent = t.name;
    chip.appendChild(label);
    const count = document.createElement('span');
    count.className = 'suggestion-count';
    count.textContent = String(t.count);
    chip.appendChild(count);
    chip.addEventListener('click', function () { commitTag(t.name); });
    list.appendChild(chip);
  });

  if (canCreate) {
    const create = document.createElement('button');
    create.type = 'button';
    create.className = 'create-chip';
    create.textContent = '+ create ' + query;
    create.addEventListener('click', function () { commitTag(query); });
    list.appendChild(create);
  }
}

export function commitFirstSuggestionOrCreate() {
  const query = normalizeTagClient(qs('field-tag-input').value);
  if (!query) return;
  const candidates = computeCandidates(query);
  commitTag(candidates.length ? candidates[0].name : query);
}

export function scrollTagClusterIntoView() {
  qs('tag-cluster').scrollIntoView({ block: 'nearest', behavior: 'smooth' });
}
