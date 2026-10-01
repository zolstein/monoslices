import { readFile, writeFile, readdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkFrontmatter from "remark-frontmatter";
import remarkSmartypants from "remark-smartypants";
import remarkStringify from "remark-stringify";
import * as prettier from "prettier";

const root = fileURLToPath(new URL("../", import.meta.url));
const config = JSON.parse(await readFile(path.join(root, ".prettierrc.json"), "utf8"));
const processor = unified()
  .use(remarkParse)
  .use(remarkGfm)
  .use(remarkFrontmatter, ["yaml", "toml"])
  .use(remarkSmartypants, { dashes: "oldschool" })
  .use(remarkStringify, { bullet: "-", fences: true, listItemIndent: "one" });

export async function formatMarkdown(source) {
  const markdown = String(await processor.process(source));
  return prettier.format(markdown, { ...config, parser: "markdown" });
}

async function markdownFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const name = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await markdownFiles(name));
    else if (entry.isFile() && name.endsWith(".md")) files.push(name);
  }
  return files.sort();
}

async function main() {
  const [mode] = process.argv.slice(2);
  if (mode === "--stdin") {
    let source = "";
    process.stdin.setEncoding("utf8");
    for await (const chunk of process.stdin) source += chunk;
    process.stdout.write(await formatMarkdown(source));
    return;
  }
  if (mode !== "--write" && mode !== "--check") {
    throw new Error("Usage: node scripts/format-markdown.mjs --stdin|--write|--check");
  }
  const files = [path.join(root, "README.md"), ...await markdownFiles(path.join(root, "docs"))];
  for (const file of files) {
    const source = await readFile(file, "utf8");
    const formatted = await formatMarkdown(source);
    if (formatted === source) continue;
    if (mode === "--write") await writeFile(file, formatted);
    else {
      console.error(`Needs formatting: ${path.relative(root, file)}`);
      process.exitCode = 1;
    }
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
