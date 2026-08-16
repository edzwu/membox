/* Review feed — X-style browsing of past note cards with spaced-review
   ratings and inline replies (方案 A: append to the note file). */

import { elements } from '../js/dom.js';
import { showToast } from '../js/ui/feedback.js';
import { session } from './session.js';

const REVIEW_ICON =
  '<svg viewBox="0 0 24 24" width="17" height="17" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M4 6h16M4 12h10M4 18h7"/>' +
  '<path fill="currentColor" d="M17 13.5l.8 2.2 2.2.8-2.2.8-.8 2.2-.8-2.2-2.2-.8 2.2-.8.8-2.2z"/>' +
  '</svg>';

const REPLY_ICON =
  '<svg viewBox="0 0 24 24" width="15" height="15" aria-hidden="true">' +
  '<path fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" ' +
  'd="M9 14 4 9l5-5M4 9h11a5 5 0 0 1 5 5v6"/>' +
  '</svg>';

const GRADES = [
  { key: 'again', label: '忘了', className: 'review-grade-again' },
  { key: 'hard', label: '模糊', className: 'review-grade-hard' },
  { key: 'good', label: '想起', className: 'review-grade-good' },
];

let reviewContainer = null;
let reviewButton = null;

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function relativeTime(ms) {
  if (!ms) return '';
  const diff = Date.now() - ms;
  const minutes = Math.floor(diff / 60000);
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;
  const d = new Date(ms);
  return `${d.getMonth() + 1}月${d.getDate()}日`;
}

function kindTag(kind) {
  if (kind === 'qa') return '<span class="review-kind review-kind-qa">Q&A</span>';
  return '';
}

function renderMeta(card) {
  const parts = [];
  const target = card.target_title || card.target_id.slice(0, 8);
  if (card.source_url) {
    parts.push(`<a href="${escapeHTML(card.source_url)}" target="_blank" rel="noopener noreferrer">${escapeHTML(target)}</a>`);
  } else {
    parts.push(`<span>${escapeHTML(target)}</span>`);
  }
  parts.push(`<span>${relativeTime(card.created_at)}</span>`);
  if (card.is_new) parts.push('<span class="review-badge-new">新</span>');
  else if (card.is_due) parts.push('<span class="review-badge-due">到期</span>');
  return parts.join(' · ');
}

function renderBody(card) {
  // Body is the note markdown (front matter stripped server-side). The quote
  // is rendered as the card headline; the rest as the note prose.
  let body = card.body || '';
  // Strip the leading quote line (already shown as the headline) to avoid
  // duplicating it.
  const lines = body.split('\n');
  if (lines.length && lines[0].trim().startsWith('>')) {
    lines.shift();
  }
  body = lines.join('\n').trim();
  if (!body) return '';
  const holder = document.createElement('div');
  holder.className = 'review-body-raw';
  holder.textContent = body; // plain text: membox notes are short, no full md render needed
  return holder.outerHTML;
}

function gradeSummary(card) {
  if (card.reps === 0) return '';
  const parts = [`复习 ${card.reps} 次`];
  if (card.interval_days > 0) parts.push(`间隔 ${card.interval_days} 天`);
  if (card.lapses > 0) parts.push(`遗忘 ${card.lapses} 次`);
  return `<span class="review-schedule">${parts.join(' · ')}</span>`;
}

function cardHTML(card) {
  return (
    '<article class="review-card" data-note-id="' + escapeHTML(card.note_id) + '">' +
      '<header class="review-card-head">' +
        `<span class="review-card-meta">${renderMeta(card)}</span>` +
        `${kindTag(card.kind)}` +
      '</header>' +
      (card.quote
        ? `<blockquote class="review-quote">${escapeHTML(card.quote)}</blockquote>`
        : '') +
      `<div class="review-card-body">${renderBody(card)}</div>` +
      `<footer class="review-card-foot">` +
        `<span class="review-actions">` +
          GRADES.map((g) =>
            `<button type="button" class="review-grade ${g.className}" data-grade="${g.key}">${g.label}</button>`,
          ).join('') +
        `</span>` +
        `<button type="button" class="review-reply-toggle" title="跟帖" aria-label="跟帖">${REPLY_ICON}</button>` +
        (card.replies_count ? `<span class="review-reply-count">${card.replies_count}</span>` : '') +
        `<span class="review-card-grades">${gradeSummary(card)}</span>` +
        `<a class="review-open" href="/?id=${encodeURIComponent(card.target_id)}" title="打开原文">原文 →</a>` +
      '</footer>' +
      '<div class="review-reply-box" hidden>' +
        '<textarea class="review-reply-input" rows="2" placeholder="跟帖：补充想法…"></textarea>' +
        '<div class="review-reply-actions">' +
          '<button type="button" class="review-reply-send">发送</button>' +
        '</div>' +
      '</div>' +
    '</article>'
  );
}

async function fetchQueue() {
  const response = await fetch('/api/review/queue?limit=40', { cache: 'no-store' });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

async function rateCard(noteID, grade) {
  const response = await fetch('/api/review/rate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note_id: noteID, grade }),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

async function replyToCard(noteID, reply) {
  const response = await fetch('/api/review/reply', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note_id: noteID, reply }),
  });
  if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
  return response.json();
}

function renderStats(stats) {
  if (!stats || !stats.all) return '还没有笔记卡片';
  const parts = [`共 ${stats.all}`];
  if (stats.new > 0) parts.push(`新 ${stats.new}`);
  if (stats.due > 0) parts.push(`到期 ${stats.due}`);
  return parts.join(' · ');
}

export function openReviewFeed() {
  // Remember the doc we came from so "原文" style navigation and back work.
  const url = new URL(window.location.href);
  url.searchParams.set('view', 'review');
  window.history.pushState({ view: 'review' }, '', url);

  if (!reviewContainer) {
    reviewContainer = document.createElement('div');
    reviewContainer.id = 'review-feed';
    document.querySelector('.layout')?.appendChild(reviewContainer);
  }
  reviewContainer.hidden = false;
  reviewContainer.innerHTML =
    '<div class="review-head">' +
      '<button type="button" class="review-back" title="返回" aria-label="返回">←</button>' +
      '<span class="review-title">复习流</span>' +
      '<span class="review-stats" id="review-stats"></span>' +
    '</div>' +
    '<div class="review-list" id="review-list"><p class="review-loading">加载中…</p></div>';

  const list = reviewContainer.querySelector('#review-list');
  const statsEl = reviewContainer.querySelector('#review-stats');
  reviewContainer.querySelector('.review-back').addEventListener('click', closeReviewFeed);

  // Hide the reading chrome while reviewing.
  elements.body.classList.add('is-reviewing');
  document.getElementById('toc')?.classList.add('is-hidden');

  void fetchQueue().then((data) => {
    statsEl.textContent = renderStats(data.stats);
    list.innerHTML = '';
    if (!data.cards || !data.cards.length) {
      list.innerHTML = '<p class="review-empty">没有可复习的笔记。先在文档里选中文字，用标注或 Q&A 生成卡片。</p>';
      return;
    }
    data.cards.forEach((card) => {
      const article = document.createElement('div');
      article.innerHTML = cardHTML(card);
      const el = article.firstElementChild;
      bindCard(el, card);
      list.appendChild(el);
    });
  }).catch((err) => {
    list.innerHTML = `<p class="review-empty">加载失败：${escapeHTML(err.message)}</p>`;
  });
}

function bindCard(el, card) {
  // Long note bodies collapse to a fixed height with a fade; clicking the
  // body toggles full expansion (the card's 原文 link opens the full note).
  const bodyEl = el.querySelector('.review-card-body');
  if (bodyEl) {
    const applyTruncate = () => {
      const collapsed = !bodyEl.classList.contains('is-expanded');
      const overflowing = bodyEl.scrollHeight > bodyEl.clientHeight + 4;
      bodyEl.classList.toggle('is-truncated', collapsed && overflowing);
      bodyEl.classList.toggle('can-expand', overflowing);
    };
    // Measure after the card is in the document (fonts/layout resolved).
    requestAnimationFrame(() => requestAnimationFrame(applyTruncate));
    bodyEl.addEventListener('click', () => {
      if (!bodyEl.classList.contains('can-expand')) return;
      bodyEl.classList.toggle('is-expanded');
      bodyEl.classList.toggle('is-truncated', !bodyEl.classList.contains('is-expanded'));
    });
  }
  el.querySelectorAll('.review-grade').forEach((btn) => {
    btn.addEventListener('click', async () => {
      btn.disabled = true;
      try {
        const schedule = await rateCard(card.note_id, btn.dataset.grade);
        const updated = { ...card, ...schedule };
        // Refresh the card's schedule summary in place.
        const foot = el.querySelector('.review-card-foot');
        const summary = foot.querySelector('.review-card-grades');
        if (summary) summary.innerHTML = gradeSummary(updated);
        showToast(btn.dataset.grade === 'good' ? '想起 ✓' : btn.dataset.grade === 'hard' ? '模糊 ~' : '忘了 ✗');
      } catch (err) {
        showToast(err.message || '评分失败');
        btn.disabled = false;
      }
    });
  });

  const toggle = el.querySelector('.review-reply-toggle');
  const box = el.querySelector('.review-reply-box');
  const input = el.querySelector('.review-reply-input');
  const send = el.querySelector('.review-reply-send');
  toggle.addEventListener('click', () => {
    box.hidden = !box.hidden;
    if (!box.hidden) input.focus();
  });
  const submit = async () => {
    const reply = input.value.trim();
    if (!reply) return;
    send.disabled = true;
    try {
      const view = await replyToCard(card.note_id, reply);
      showToast('跟帖已追加到笔记');
      input.value = '';
      box.hidden = true;
      const countEl = el.querySelector('.review-reply-count');
      if (view.replies_count) {
        if (!countEl) {
          const f = el.querySelector('.review-card-foot');
          const toggleBtn = f.querySelector('.review-reply-toggle');
          const span = document.createElement('span');
          span.className = 'review-reply-count';
          span.textContent = view.replies_count;
          toggleBtn.insertAdjacentElement('afterend', span);
        } else {
          countEl.textContent = view.replies_count;
        }
      }
      // Re-render the body so the appended reply shows inline.
      const bodyEl = el.querySelector('.review-card-body');
      if (bodyEl) bodyEl.innerHTML = renderBody(view);
    } catch (err) {
      showToast(err.message || '跟帖失败');
      send.disabled = false;
    }
  };
  send.addEventListener('click', submit);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && !e.isComposing) {
      e.preventDefault();
      void submit();
    }
    if (e.key === 'Escape' && !e.isComposing) box.hidden = true;
  });
}

export function closeReviewFeed() {
  if (reviewContainer) reviewContainer.hidden = true;
  elements.body.classList.remove('is-reviewing');
  document.getElementById('toc')?.classList.remove('is-hidden');
  const url = new URL(window.location.href);
  url.searchParams.delete('view');
  window.history.replaceState(null, '', url);
}

export function isReviewView() {
  return new URLSearchParams(window.location.search).get('view') === 'review';
}

export function initReview() {
  reviewButton = document.createElement('button');
  reviewButton.type = 'button';
  reviewButton.id = 'membox-review';
  reviewButton.className = 'action membox-review';
  reviewButton.innerHTML = REVIEW_ICON + '<span class="sr-only">复习流</span>';
  reviewButton.title = '复习流 — 浏览笔记卡片';
  reviewButton.setAttribute('aria-label', '复习流');
  elements.themeToggle.insertAdjacentElement('beforebegin', reviewButton);
  reviewButton.addEventListener('click', () => {
    if (!session.connected) {
      showToast('先连接 membox');
      return;
    }
    if (isReviewView()) closeReviewFeed();
    else openReviewFeed();
  });

  // Browser back from ?view=review closes the feed cleanly.
  window.addEventListener('popstate', () => {
    if (!isReviewView() && reviewContainer) reviewContainer.hidden = true;
  });

  if (isReviewView()) {
    openReviewFeed();
  }
}
