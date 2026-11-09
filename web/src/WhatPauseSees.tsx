import { useEffect, useRef, useState } from 'react'
import { api } from './api'

export type Explanation = {
 source: 'AI' | 'TEMPLATE'; model?: string; prompt_version?: string; reason_code?: string
 sentences: { text: string; fact_ids: string[] }[]
 facts: { id: string; label: string; text: string }[]
}

export function WhatPauseSeesView({ explanation: e }: { explanation: Explanation }) {
 const facts = new Map(e.facts.map(f => [f.id, f]))
 return <section className="source-card what-pause-sees" aria-label="What Pause sees" data-source={e.source}>
  <h4>What Pause sees</h4>
  <p className="helper">{e.source === 'AI' ? 'Written by AI (Gemini) from the checks below. It can be wrong; the checks themselves are what count.' : 'Standard summary from the checks below.'}</p>
  {e.sentences.map((s, index) => <div key={index}><p>{s.text}</p><div className="explanation-facts">{s.fact_ids.map(id => {
   const fact = facts.get(id)
   return fact ? <small key={id} className="explanation-chip" title={fact.text}>{fact.label}</small> : null
  })}</div></div>)}
 </section>
}

export function WhatPauseSees({ intentID, evidenceID }: { intentID: string; evidenceID: string }) {
 const sequence = useRef(0)
 const [result, setResult] = useState<{ key: string; explanation?: Explanation; error?: boolean } | null>(null)
 const key = JSON.stringify([intentID, evidenceID])
 useEffect(() => {
  const seq = ++sequence.current, controller = new AbortController()
  setResult(null)
  void api<Explanation>(`/intents/${intentID}/explanation?evidence_id=${encodeURIComponent(evidenceID)}`, 'GET', undefined, undefined, controller.signal).then(explanation => {
   if (sequence.current === seq && !controller.signal.aborted) setResult({ key, explanation })
  }).catch(() => { if (sequence.current === seq && !controller.signal.aborted) setResult({ key, error: true }) })
  return () => { sequence.current++; controller.abort() }
 }, [intentID, evidenceID, key])
 if (result?.key !== key) return <p className="helper" role="status">Preparing a short summary of the checks…</p>
 return result.explanation ? <WhatPauseSeesView explanation={result.explanation} /> : null
}
