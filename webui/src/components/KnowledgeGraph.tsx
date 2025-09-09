import React, { useEffect, useMemo, useRef, useState } from 'react'

interface KGNode { id: string; label?: string; name?: string; group?: string | number }
interface KGEdge { source: string; target: string; weight?: number }

interface Props { nodes: KGNode[]; edges: KGEdge[] }

// Simple radial layout fallback with light force relaxation
export default function KnowledgeGraph({ nodes, edges }: Props) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const [hoverNode, setHoverNode] = useState<KGNode | null>(null)

  const layout = useMemo(()=>{
    const safeNodes = Array.isArray(nodes) ? nodes : []
    const safeEdges = Array.isArray(edges) ? edges : []
    if (safeNodes.length === 0) return { nodes: [], edges: safeEdges }
    const placed = safeNodes.map((n, i) => {
      const angle = (i / safeNodes.length) * Math.PI * 2
      const radius = 140 + (i % 7) * 6
      return { ...n, x: Math.cos(angle) * radius, y: Math.sin(angle) * radius }
    })
    // light iterative adjustment based on edges (pull connected nodes slightly together)
    for (let iter=0; iter<25; iter++) {
      safeEdges.forEach(e => {
        const a = placed.find(p=>p.id===e.source)
        const b = placed.find(p=>p.id===e.target)
        if (!a || !b) return
        const dx = b.x - a.x; const dy = b.y - a.y
        const dist = Math.hypot(dx, dy) || 1
        const desired = 120
        const diff = (dist - desired) / dist * 0.05
        a.x += dx * diff; a.y += dy * diff
        b.x -= dx * diff; b.y -= dy * diff
      })
    }
    return { nodes: placed, edges: safeEdges }
  }, [nodes, edges])

  useEffect(()=>{
    const canvas = canvasRef.current; if (!canvas) return
    const ctx = canvas.getContext('2d'); if (!ctx) return
    try {
      const dpr = window.devicePixelRatio || 1
      const width = canvas.clientWidth; const height = canvas.clientHeight
      canvas.width = width * dpr; canvas.height = height * dpr
      ctx.scale(dpr, dpr)
      ctx.clearRect(0,0,width,height)
      ctx.translate(width/2, height/2)

      // draw edges
      ctx.lineWidth = 1
      // slate-400 with ~35% alpha (#94a3b8 + alpha 0x59)
      ctx.strokeStyle = '#94a3b859'
      ;(layout.edges || []).forEach(e => {
        const a = layout.nodes.find(n=>n.id===e.source)
        const b = layout.nodes.find(n=>n.id===e.target)
        if(!a||!b) return
        ctx.beginPath(); ctx.moveTo((a as any).x, (a as any).y); ctx.lineTo((b as any).x, (b as any).y); ctx.stroke()
      })

      // draw nodes
      layout.nodes.forEach(n => {
        const deg = nodeDegree(layout.edges, n.id)
        const r = 6 + Math.min(10, deg * 2)
        const color = colorForGroup(n.group)
        ctx.beginPath(); ctx.fillStyle = color; ctx.arc((n as any).x, (n as any).y, r, 0, Math.PI*2); ctx.fill()
      })
    } catch (e) {
      // Avoid crashing the page if canvas API misbehaves; show a friendly fallback
      // ErrorBoundary will also catch this if it propagates
      console.error('KnowledgeGraph render error', e)
    }
  }, [layout])

  // pointer handling for hover tooltips
  useEffect(()=>{
    const canvas = canvasRef.current; if (!canvas) return
    function onMove(ev: MouseEvent) {
      if (!canvas) return
      const rect = canvas.getBoundingClientRect()
      const x = ev.clientX - rect.left - rect.width/2
      const y = ev.clientY - rect.top - rect.height/2
      const hit = layout.nodes.find(n => Math.hypot((n as any).x - x, (n as any).y - y) < 14)
      setHoverNode(hit || null)
    }
    function onLeave() { setHoverNode(null) }
    canvas.addEventListener('mousemove', onMove)
    canvas.addEventListener('mouseleave', onLeave)
    return ()=>{ canvas.removeEventListener('mousemove', onMove); canvas.removeEventListener('mouseleave', onLeave) }
  }, [layout])

  return (
    <div className="relative w-full h-72 md:h-80 bg-slate-900/60 border border-slate-800 rounded">
      <canvas ref={canvasRef} className="w-full h-full" />
      {hoverNode && (
        <div className="pointer-events-none absolute left-2 top-2 text-xs bg-slate-800/80 backdrop-blur px-2 py-1 rounded border border-slate-700 shadow">
          <div className="font-semibold text-slate-200 truncate max-w-[220px]">{hoverNode.label || hoverNode.name || hoverNode.id}</div>
          <div className="text-slate-400">Degree {nodeDegree(layout.edges, hoverNode.id)}</div>
        </div>
      )}
      {layout.nodes.length === 0 && <div className="absolute inset-0 flex items-center justify-center text-xs text-slate-500">No knowledge graph data</div>}
    </div>
  )
}

function nodeDegree(edges: KGEdge[], id: string) { return edges.reduce((a,e)=> a + ((e.source===id || e.target===id)?1:0), 0) }

function colorForGroup(g: any) {
  if (g == null) return '#3b82f6'
  const colors = ['#3b82f6','#10b981','#f59e0b','#6366f1','#ec4899','#06b6d4','#84cc16','#ef4444']
  const idx = Math.abs(hash(String(g))) % colors.length
  return colors[idx]
}
function hash(s: string) { let h=0; for (let i=0;i<s.length;i++) h = (h*31 + s.charCodeAt(i))|0; return h }
