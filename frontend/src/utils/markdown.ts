import MarkdownIt from "markdown-it";
import taskLists from "markdown-it-task-lists";
import hljs from "highlight.js";

// GitHub-flavoured markdown rendering, fully bundled: no runtime asset fetching
// and no CDN, so it behaves the same on an isolated network. Styling comes from
// github-markdown-css applied to a `.markdown-body` container, and code blocks
// are highlighted here at render time with highlight.js.
//
// This module is intentionally DOM-free so it can be unit tested in Node; the
// caller is responsible for sanitising the result before inserting it.

const highlight = (code: string, lang: string): string => {
  const language = lang && hljs.getLanguage(lang) ? lang : "";
  try {
    const result = language
      ? hljs.highlight(code, { language, ignoreIllegals: true })
      : hljs.highlightAuto(code);
    const cls = language ? ` language-${language}` : "";
    return `<pre class="hljs"><code class="hljs${cls}">${result.value}</code></pre>`;
  } catch {
    return `<pre class="hljs"><code class="hljs">${md.utils.escapeHtml(code)}</code></pre>`;
  }
};

const md = new MarkdownIt({
  html: true,
  linkify: true,
  breaks: false,
  highlight,
});

md.use(taskLists, { label: true });

// Open external links in a new tab so they are not swallowed by the SPA router.
const defaultLinkOpen =
  md.renderer.rules.link_open ||
  ((tokens, idx, options, _env, self) =>
    self.renderToken(tokens, idx, options));

md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const href = String(tokens[idx].attrGet("href") ?? "");
  if (!href.startsWith("#")) {
    tokens[idx].attrSet("target", "_blank");
    tokens[idx].attrSet("rel", "noopener noreferrer");
  }
  return defaultLinkOpen(tokens, idx, options, env, self);
};

/** Renders markdown to (unsanitised) HTML. */
export function markdownToHtml(src: string): string {
  return md.render(src);
}
