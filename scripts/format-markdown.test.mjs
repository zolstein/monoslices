import assert from "node:assert/strict";
import test from "node:test";
import { formatMarkdown } from "./format-markdown.mjs";

test("smart punctuation, entities, and soft-wrapped prose", async () => {
  const result = await formatMarkdown('"Hello" --- one -- two... &mdash;\nnext line.\n');
  assert.equal(result, '“Hello” — one – two… — next line.\n');
  assert.equal(await formatMarkdown(result), result);
});

test("code, URLs, frontmatter, and thematic breaks remain literal", async () => {
  const source = '---\ntitle: "literal --- ..."\n---\n\n`"literal --- ... &mdash;"`\n\n```go\n// "literal --- ... &mdash;"\n```\n\n[link](https://example.com/a---b?q=...)\n\n---\n';
  const result = await formatMarkdown(source);
  assert.ok(result.startsWith('---\ntitle: "literal --- ..."\n---\n'));
  assert.ok(result.includes('`"literal --- ... &mdash;"`'));
  assert.ok(result.includes('// "literal --- ... &mdash;"'));
  assert.ok(result.includes('https://example.com/a---b?q=...'));
  assert.ok(result.endsWith("\n---\n"));
  assert.equal(await formatMarkdown(result), result);
});

test("tables align, hard breaks survive, and escaped syntax stays literal", async () => {
  const result = await formatMarkdown('| a | longer |\n| --- | --- |\n| x | y |\n\nfirst\\\nsecond\n\n\\*literal\\* --- end\n');
  assert.ok(result.includes('| a   | longer |'));
  assert.ok(result.includes('first\\\nsecond'));
  assert.ok(result.includes('\\*literal\\* — end'));
  assert.equal(await formatMarkdown(result), result);
});
