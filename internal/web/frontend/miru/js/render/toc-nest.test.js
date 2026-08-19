/* node --test toc-nest.test.js */
import { describe, it } from 'node:test';
import assert from 'node:assert/strict';
import { outlineDepth, nestParents, tocNestLevel, HTML_WEIGHT } from './toc-nest.js';

describe('outlineDepth', () => {
  it('parses dotted section numbers', () => {
    assert.equal(outlineDepth('10.1 多 Agent'), 2);
    assert.equal(outlineDepth('10.1.1 维度'), 3);
    assert.equal(outlineDepth('1. Introduction'), 1);
  });

  it('rejects years and huge bare ints', () => {
    assert.equal(outlineDepth('2001 年：开端'), null);
    assert.equal(outlineDepth('2026年的计划'), null);
    assert.equal(outlineDepth('第 10 章'), null);
  });
});

describe('tocNestLevel composite', () => {
  it('lets HTML dominate numbered ### under ## (ted-style notes)', () => {
    const stack = [
      { level: 0, outline: false },
      { level: 2 * HTML_WEIGHT, outline: false }, // ## parent
    ];
    const child = tocNestLevel('H3', '1. 解析时就按宽度排版', stack);
    assert.equal(child.htmlLevel, 3);
    assert.ok(child.level > stack[1].level);
  });

  it('nests PDF-flattened ## 10 / 10.1 / 10.1.1', () => {
    const items = [
      { tagName: 'H2', label: '10 多 Agent 协作' },
      { tagName: 'H2', label: '10.1 维度一' },
      { tagName: 'H2', label: '10.1.1 细节' },
      { tagName: 'H2', label: '11 下一章' },
    ];
    const parents = nestParents(items);
    assert.deepEqual(parents, [-1, 0, 1, -1]);
  });

  it('keeps unnumbered body under numbered PDF section', () => {
    const items = [
      { tagName: 'H2', label: '10.1 维度一' },
      { tagName: 'H2', label: '实验要求' },
      { tagName: 'H2', label: '10.2 维度二' },
    ];
    const parents = nestParents(items);
    assert.deepEqual(parents, [-1, 0, -1]);
  });

  it('normal markdown ## / ### without numbers', () => {
    const items = [
      { tagName: 'H2', label: 'leaf 是什么' },
      { tagName: 'H2', label: 'leaf 最关键的 5 个设计点' },
      { tagName: 'H3', label: '解析时就按宽度排版' },
      { tagName: 'H3', label: '行模型是多 Span' },
      { tagName: 'H2', label: '和之前方案的修正' },
    ];
    const parents = nestParents(items);
    assert.deepEqual(parents, [-1, -1, 1, 1, -1]);
  });

  it('numbered ### under ## stay children (the original bug)', () => {
    const items = [
      { tagName: 'H2', label: 'leaf 最关键的 5 个设计点' },
      { tagName: 'H3', label: '1. 解析时就按宽度排版' },
      { tagName: 'H3', label: '2. 行模型是多 Span' },
      { tagName: 'H2', label: '和之前方案的修正' },
    ];
    const parents = nestParents(items);
    // Old outline-only: [ -1, -1, -1, -1 ] (all top-level)
    // Composite: numbered h3 nest under first h2
    assert.deepEqual(parents, [-1, 0, 0, -1]);
  });
});
