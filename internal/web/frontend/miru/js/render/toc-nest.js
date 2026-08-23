/* Pure TOC nesting — no DOM. Used by toc.js and unit tests.
 *
 * Strategy: HTML heading level is the major key; dotted outline numbers in the
 * title (PDF-flattened "## 10.1 …") are a minor key within the same HTML rank.
 *
 *   level = htmlLevel * HTML_WEIGHT + (outlineDepth || 0)
 *
 * This keeps normal Markdown (## / ### 1. foo) hierarchical, and still nests
 * PDF-style same-tag outlines (## 10 / ## 10.1 / ## 10.1.1).
 */

export const HTML_WEIGHT = 100;

// "10.1 多 Agent…" → 2, "10.1.1 …" → 3, "Part 0 — …" → 1, "第 10 章" → null.
// Reject years ("2001 年") and large bare integers.
export function outlineDepth(label) {
  const text = String(label || '').trim();

  // Spec-style major sections: "Part 0 — Orientation", "Chapter 2: Foo".
  // Depth 0 ⇒ same HTML slot as a bare H2 (peers of a subtitle like
  // "## AgentHarness — …"), while still outline:true so "## 0.7" does not
  // swallow the next "## Part 1" via the unnumbered-under-outline rule.
  // Depth 1 would bury every Part under that subtitle (not equivalent peers).
  if (/^(?:Part|Chapter)\s+\d+\b/i.test(text)) return 0;

  const match = text.match(/^(\d+(?:\.\d+)*)\b/);
  if (!match) return null;

  const after = text.slice(match[0].length);
  if (/^\s*年/.test(after)) return null;

  const parts = match[1].split('.');
  if (parts.some((part) => part.length > 2)) return null;

  return parts.length;
}

/**
 * @param {string} tagName e.g. "H2"
 * @param {string} label heading text
 * @param {Array<{level:number, outline:boolean}>} stack build stack (root first)
 */
export function tocNestLevel(tagName, label, stack = [{ level: 0, outline: false, htmlLevel: 0 }]) {
  const htmlLevel = Math.min(6, Math.max(1, Number(String(tagName).replace(/\D/g, '')) || 1));
  const outline = outlineDepth(label);

  if (outline != null) {
    return {
      level: htmlLevel * HTML_WEIGHT + outline,
      outline: true,
      htmlLevel,
    };
  }

  // Unnumbered body under a numbered PDF-style section stays nested
  // ("实验要求" under "10.1") instead of jumping to a bare HTML peer slot.
  // Only when HTML rank is same or deeper — an h2 after numbered h3 must
  // NOT tuck under the h3 (that was the reverse of the original bug).
  for (let i = stack.length - 1; i >= 1; i -= 1) {
    if (!stack[i].outline) continue;
    const parentHtml = stack[i].htmlLevel != null
      ? stack[i].htmlLevel
      : (Math.floor(stack[i].level / HTML_WEIGHT) || 0);
    if (htmlLevel >= parentHtml) {
      return {
        level: stack[i].level + 1,
        outline: false,
        htmlLevel,
      };
    }
    break;
  }

  return {
    level: htmlLevel * HTML_WEIGHT,
    outline: false,
    htmlLevel,
  };
}

/** Simulate buildToc parent assignment; returns array of parent indices (-1 = root). */
export function nestParents(items) {
  // items: [{ tagName, label }]
  const stack = [{ level: 0, outline: false, htmlLevel: 0, index: -1 }];
  const parents = [];

  items.forEach((item, index) => {
    const { level, outline, htmlLevel } = tocNestLevel(item.tagName, item.label, stack);
    while (stack.length > 1 && stack[stack.length - 1].level >= level) {
      stack.pop();
    }
    parents.push(stack[stack.length - 1].index);
    stack.push({ level, outline, htmlLevel, index });
  });

  return parents;
}
