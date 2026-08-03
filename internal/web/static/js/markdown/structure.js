/* Miru — split raw Markdown into the leading (pre-heading) source and one
   entry per H1–H3 section, keyed by source order. Powers per-section
   copy/download and the fold-section source attribution. Pure text in,
   data out — no DOM. */

export function parseMarkdownStructure(text) {
  const lines = text.split('\n');
  const leadingLines = [];
  const sections = [];
  const headings = [];
  let inCodeBlock = false;
  let codeFence = '';

  // First pass: collect headings and leading lines, respecting code fences.
  lines.forEach((line, index) => {
    const fenceMatch = line.match(/^(\s*)(```|~~~)/);
    if (fenceMatch) {
      if (!inCodeBlock) {
        inCodeBlock = true;
        codeFence = fenceMatch[2];
      } else if (line.trim().startsWith(codeFence)) {
        inCodeBlock = false;
        codeFence = '';
      }
    }

    const headingMatch = !inCodeBlock && line.match(/^(#{1,3})\s+(.*)$/);
    if (headingMatch) {
      headings.push({
        level: headingMatch[1].length,
        lineIndex: index,
        heading: headingMatch[2].trim(),
      });
      return;
    }

    if (headings.length === 0) {
      leadingLines.push(line);
    }
  });

  // Second pass: each section runs from its heading up to (but not including)
  // the next heading of the same or higher level. This makes a parent section
  // (e.g. ##) include the full source of its descendants (e.g. ###).
  for (let i = 0; i < headings.length; i++) {
    const current = headings[i];
    const start = current.lineIndex;
    let end = lines.length;

    for (let j = i + 1; j < headings.length; j++) {
      if (headings[j].level <= current.level) {
        end = headings[j].lineIndex;
        break;
      }
    }

    sections.push({
      heading: current.heading,
      source: lines.slice(start, end).join('\n'),
    });
  }

  return {
    leadingSource: leadingLines.join('\n').trim(),
    sections,
  };
}
