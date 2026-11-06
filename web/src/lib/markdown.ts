import DOMPurify from 'dompurify'
import hljs from 'highlight.js'
import MarkdownIt from 'markdown-it'
import type { Heading } from '@/types'

interface RenderEnvironment {
  docPath: string
  resolveAsset: (path: string) => string
  headings: Heading[]
  slugs: Map<string, number>
}

export interface RenderedMarkdown {
  html: string
  headings: Heading[]
}

export function renderMarkdown(
  source: string,
  docPath: string,
  resolveAsset: (path: string) => string,
): RenderedMarkdown {
  const markdown = new MarkdownIt({
    html: false,
    linkify: true,
    typographer: true,
    highlight(code, language) {
      if (language && hljs.getLanguage(language)) {
        return `<pre class="hljs"><code class="language-${escapeHtml(language)}">${hljs.highlight(code, { language }).value}</code></pre>`
      }
      return `<pre class="hljs"><code>${escapeHtml(code)}</code></pre>`
    },
  })

  const defaultImage = markdown.renderer.rules.image!
  markdown.renderer.rules.image = (tokens, index, options, env, self) => {
    const context = env as RenderEnvironment
    const source = tokens[index].attrGet('src') ?? ''
    if (source && isRelativeURL(source)) {
      tokens[index].attrSet('src', context.resolveAsset(resolveRelative(docPath, source)))
      tokens[index].attrSet('loading', 'lazy')
    }
    return defaultImage(tokens, index, options, context, self)
  }

  const defaultLinkOpen = markdown.renderer.rules.link_open ??
    ((tokens, index, options, _env, self) => self.renderToken(tokens, index, options))
  markdown.renderer.rules.link_open = (tokens, index, options, env, self) => {
    const context = env as RenderEnvironment
    const href = tokens[index].attrGet('href') ?? ''
    if (href && isRelativeURL(href)) {
      const [hrefWithoutAnchor, anchor] = href.split('#', 2)
      const target = resolveRelative(docPath, hrefWithoutAnchor)
      if (isMarkdown(target)) {
        tokens[index].attrSet('href', '#')
        tokens[index].attrSet('data-doc-path', target)
        if (anchor) tokens[index].attrSet('data-doc-anchor', anchor)
        tokens[index].attrSet('class', 'internal-doc-link')
      } else {
        tokens[index].attrSet('href', context.resolveAsset(target))
        tokens[index].attrSet('target', '_blank')
        tokens[index].attrSet('rel', 'noopener noreferrer')
      }
    } else if (/^https?:\/\//i.test(href)) {
      tokens[index].attrSet('target', '_blank')
      tokens[index].attrSet('rel', 'noopener noreferrer')
    }
    return defaultLinkOpen(tokens, index, options, context, self)
  }

  const defaultHeadingOpen = markdown.renderer.rules.heading_open ??
    ((tokens, index, options, _env, self) => self.renderToken(tokens, index, options))
  markdown.renderer.rules.heading_open = (tokens, index, options, env, self) => {
    const context = env as RenderEnvironment
    const level = Number(tokens[index].tag.slice(1))
    if (level >= 2 && level <= 4) {
      const inline = tokens[index + 1]
      const text = inline?.content?.trim() || '未命名章节'
      const id = uniqueSlug(text, context.slugs)
      tokens[index].attrSet('id', id)
      context.headings.push({ id, text, level })
    }
    return defaultHeadingOpen(tokens, index, options, context, self)
  }

  const defaultFence = markdown.renderer.rules.fence ??
    ((tokens, index, _options, _env, self) => self.renderToken(tokens, index, _options))
  markdown.renderer.rules.fence = (tokens, index, options, env, self) => {
    const language = tokens[index].info.trim().split(/\s+/)[0]?.toLowerCase()
    if (language === 'mermaid') {
      return `<div class="mermaid">${escapeHtml(tokens[index].content)}</div>`
    }
    return defaultFence(tokens, index, options, env as RenderEnvironment, self)
  }

  const environment: RenderEnvironment = {
    docPath,
    resolveAsset,
    headings: [],
    slugs: new Map(),
  }
  const rendered = markdown.render(source, environment)
  return {
    html: DOMPurify.sanitize(rendered, {
      ADD_ATTR: ['target', 'rel', 'data-doc-path', 'data-doc-anchor'],
    }),
    headings: environment.headings,
  }
}

function uniqueSlug(text: string, slugs: Map<string, number>): string {
  const base = text
    .toLowerCase()
    .trim()
    .replace(/[`*_~[\](){}<>]/g, '')
    .replace(/[^\p{L}\p{N}\s-]/gu, '')
    .replace(/\s+/g, '-') || 'section'
  const count = slugs.get(base) ?? 0
  slugs.set(base, count + 1)
  return count === 0 ? base : `${base}-${count + 1}`
}

function isRelativeURL(value: string): boolean {
  return !/^(?:[a-z][a-z0-9+.-]*:|\/\/|#)/i.test(value)
}

function isMarkdown(value: string): boolean {
  return /\.md(?:[?#].*)?$/i.test(value) || !/\.[a-z0-9]{1,8}(?:[?#].*)?$/i.test(value)
}

function resolveRelative(docPath: string, target: string): string {
  const cleanTarget = target.split('#')[0].split('?')[0]
  const segments = docPath.split('/')
  segments.pop()
  for (const part of cleanTarget.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') segments.pop()
    else segments.push(part)
  }
  return segments.join('/')
}

function escapeHtml(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#039;')
}
