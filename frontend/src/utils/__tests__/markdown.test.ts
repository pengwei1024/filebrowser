import { describe, expect, it } from "vitest";
import { markdownToHtml } from "../markdown";

describe("markdownToHtml", () => {
  it("highlights fenced code blocks with a declared language", () => {
    const html = markdownToHtml("```js\nconst a = 1;\n```");
    expect(html).toContain('<pre class="hljs">');
    expect(html).toContain("language-js");
    // highlight.js emits span wrappers for the tokens it recognises
    expect(html).toContain("hljs-keyword");
  });

  it("falls back to auto-detection for unlabelled code blocks", () => {
    const html = markdownToHtml("```\n<?php echo 1; ?>\n```");
    expect(html).toContain('<pre class="hljs">');
    expect(html).toContain("<span");
  });

  it("renders GFM tables and task lists", () => {
    const table = markdownToHtml("| a | b |\n| - | - |\n| 1 | 2 |");
    expect(table).toContain("<table>");

    const tasks = markdownToHtml("- [x] done\n- [ ] todo");
    expect(tasks).toContain('type="checkbox"');
  });

  it("opens external links in a new tab but leaves anchors alone", () => {
    const external = markdownToHtml("[x](https://example.com)");
    expect(external).toContain('target="_blank"');
    expect(external).toContain('rel="noopener noreferrer"');

    const anchor = markdownToHtml("[x](#section)");
    expect(anchor).not.toContain('target="_blank"');
  });

  it("autolinks bare URLs", () => {
    expect(markdownToHtml("see https://example.com")).toContain("<a");
  });
});
