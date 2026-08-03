/* Miru — YAML-ish frontmatter: parse the `---` header block and render it
   as the article metadata card (created / tags / source / author + any
   extra keys). */

import { elements } from '../dom.js';

export function parseFrontmatter(text) {
  const lines = text.split('\n');
  if (lines.length < 2 || lines[0].trim() !== '---') {
    return { meta: null, body: text };
  }

  let end = -1;
  for (let i = 1; i < lines.length; i++) {
    if (lines[i].trim() === '---') {
      end = i;
      break;
    }
  }

  if (end === -1) {
    return { meta: null, body: text };
  }

  const meta = {};
  for (let i = 1; i < end; i++) {
    const line = lines[i];
    const idx = line.indexOf(':');
    if (idx === -1) continue;
    const key = line.slice(0, idx).trim();
    const value = line.slice(idx + 1).trim();
    if (key) meta[key] = value;
  }

  const body = lines.slice(end + 1).join('\n');
  return { meta, body };
}

function formatFrontmatterDate(value) {
  const iso = value.match(/(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})/);
  if (iso) {
    const d = new Date(iso[1]);
    if (!isNaN(d.getTime())) {
      return d.toLocaleString('zh-CN', { dateStyle: 'medium', timeStyle: 'short' });
    }
  }
  return value;
}

function parseFrontmatterTags(value) {
  if (!value || value === '[]') return [];
  try {
    const parsed = JSON.parse(value);
    if (Array.isArray(parsed)) return parsed.filter(Boolean);
  } catch {}
  return value.split(',').map((s) => s.trim()).filter(Boolean);
}

export function renderFrontmatter(meta) {
  const entries = Object.entries(meta).filter(([_, value]) => value !== '');
  if (entries.length === 0) return;

  const card = document.createElement('div');
  card.className = 'article-meta';

  const rendered = new Set();

  const renderRow = (label, valueNode) => {
    const row = document.createElement('div');
    row.className = 'article-meta-row';

    const labelEl = document.createElement('span');
    labelEl.className = 'article-meta-label';
    labelEl.textContent = label;
    row.appendChild(labelEl);

    const valueEl = document.createElement('span');
    valueEl.className = 'article-meta-value';
    if (typeof valueNode === 'string') {
      valueEl.textContent = valueNode;
    } else {
      valueEl.appendChild(valueNode);
    }
    row.appendChild(valueEl);
    return row;
  };

  if (meta.created) {
    card.appendChild(renderRow('Created', formatFrontmatterDate(meta.created)));
    rendered.add('created');
  }

  if (meta.tags) {
    const tags = parseFrontmatterTags(meta.tags);
    if (tags.length) {
      const row = document.createElement('div');
      row.className = 'article-meta-row';
      const labelEl = document.createElement('span');
      labelEl.className = 'article-meta-label';
      labelEl.textContent = 'Tags';
      row.appendChild(labelEl);
      const valueEl = document.createElement('span');
      valueEl.className = 'article-meta-value';
      tags.forEach((tag) => {
        const pill = document.createElement('span');
        pill.className = 'article-meta-tag';
        pill.textContent = tag;
        valueEl.appendChild(pill);
      });
      row.appendChild(valueEl);
      card.appendChild(row);
    }
    rendered.add('tags');
  }

  if (meta.source) {
    const link = document.createElement('a');
    link.href = meta.source;
    try {
      const url = new URL(meta.source);
      link.textContent = url.hostname;
    } catch {
      link.textContent = meta.source;
    }
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    card.appendChild(renderRow('Source', link));
    rendered.add('source');
  }

  if (meta.author) {
    card.appendChild(renderRow('Author', meta.author));
    rendered.add('author');
  }

  entries.forEach(([key, value]) => {
    if (rendered.has(key)) return;
    card.appendChild(renderRow(key, value));
  });

  elements.article.insertBefore(card, elements.article.firstChild);
}
