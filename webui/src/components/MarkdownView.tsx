import React from 'react'

// Minimal Markdown renderer without external deps. Supports:
// - Headings (# .. ######)
// - Paragraphs, blank lines
// - Bulleted/numbered lists (-, *, 1.)
// - Code fences ``` and inline code `code`
// - Links [text](url)
// - Bold/Italic (**text**, *text*)
// It is intentionally conservative: no raw HTML support.

function tokenize(text: string): any[] {
  const lines = text.split(/\r?\n/)
  const tokens: any[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    // code fence
    if (/^```/.test(line)) {
      const fence = line.replace(/^```\s*/, '')
      const buf: string[] = []
      i++
      while (i < lines.length && !/^```/.test(lines[i])) { buf.push(lines[i]); i++ }
      if (i < lines.length && /^```/.test(lines[i])) i++
      tokens.push({ type: 'code', lang: fence || '', content: buf.join('\n') })
      continue
    }
    // heading
    const m = /^(#{1,6})\s+(.*)$/.exec(line)
    if (m) { tokens.push({ type: 'heading', depth: m[1].length, text: m[2] }); i++; continue }
    // list (ul)
    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) { items.push(lines[i].replace(/^\s*[-*]\s+/, '')); i++ }
      tokens.push({ type: 'ul', items })
      continue
    }
    // list (ol)
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) { items.push(lines[i].replace(/^\s*\d+\.\s+/, '')); i++ }
      tokens.push({ type: 'ol', items })
      continue
    }
    // blank line
    if (/^\s*$/.test(line)) { tokens.push({ type: 'blank' }); i++; continue }
    // paragraph: accumulate until blank or other block
    const buf: string[] = []
    while (i < lines.length && !/^\s*$/.test(lines[i]) && !/^(#{1,6})\s+/.test(lines[i]) && !/^```/.test(lines[i]) && !/^\s*[-*]\s+/.test(lines[i]) && !/^\s*\d+\.\s+/.test(lines[i])) {
      buf.push(lines[i]); i++
    }
    if (buf.length) tokens.push({ type: 'p', text: buf.join('\n') })
  }
  return tokens
}

function inlineParts(text: string): React.ReactNode[] {
  const parts: React.ReactNode[] = []
  let remaining = text
  const pushText = (t: string) => parts.push(<span key={parts.length}>{t}</span>)
  // simple loop for links + code + bold/italic
  while (remaining.length) {
    const link = /\[([^\]]+)\]\(([^)]+)\)/.exec(remaining)
    const code = /`([^`]+)`/.exec(remaining)
    const bold = /\*\*([^*]+)\*\*/.exec(remaining)
    const italic = /\*([^*]+)\*/.exec(remaining)
    const next = [link, code, bold, italic].filter(Boolean).sort((a:any,b:any)=> (a!.index! - b!.index!))[0] as RegExpExecArray | undefined
    if (!next) { pushText(remaining); break }
    if (next.index! > 0) { pushText(remaining.slice(0, next.index!)) }
    const [full] = next
    if (next === link) {
      const [, label, url] = next
      parts.push(<a key={parts.length} href={url} target="_blank" rel="noopener noreferrer" className="text-brand-300 hover:underline">{label}</a>)
    } else if (next === code) {
      parts.push(<code key={parts.length} className="px-1 rounded bg-slate-800 text-[12px]">{next[1]}</code>)
    } else if (next === bold) {
      parts.push(<strong key={parts.length}>{next[1]}</strong>)
    } else if (next === italic) {
      parts.push(<em key={parts.length}>{next[1]}</em>)
    }
    remaining = remaining.slice(next.index! + full.length)
  }
  return parts
}

export default function MarkdownView({ markdown }: { markdown: string }) {
  const tokens = tokenize(markdown || '')
  const out: React.ReactNode[] = []
  for (const t of tokens) {
    if (t.type === 'blank') { out.push(<div key={out.length} className="h-2" />); continue }
    if (t.type === 'heading') {
      const Tag = (('h'+Math.min(6, Math.max(1, t.depth))) as keyof JSX.IntrinsicElements)
      out.push(<Tag key={out.length} className="mt-4 mb-2 font-semibold text-slate-200">{inlineParts(t.text)}</Tag>)
      continue
    }
    if (t.type === 'ul') {
      out.push(<ul key={out.length} className="list-disc pl-5 space-y-1">{t.items.map((li:string,i:number)=>(<li key={i}>{inlineParts(li)}</li>))}</ul>)
      continue
    }
    if (t.type === 'ol') {
      out.push(<ol key={out.length} className="list-decimal pl-5 space-y-1">{t.items.map((li:string,i:number)=>(<li key={i}>{inlineParts(li)}</li>))}</ol>)
      continue
    }
    if (t.type === 'code') {
      out.push(
        <pre key={out.length} className="bg-slate-900/70 border border-slate-800 rounded p-3 overflow-auto text-[12px]"><code>{t.content}</code></pre>
      )
      continue
    }
    if (t.type === 'p') {
      out.push(<p key={out.length} className="leading-relaxed text-sm text-slate-200 whitespace-pre-wrap">{inlineParts(t.text)}</p>)
    }
  }
  return <div className="prose prose-invert max-w-none">{out}</div>
}
