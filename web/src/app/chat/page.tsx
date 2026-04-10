'use client'

import { MessageSquare, Sparkles, ArrowRight } from 'lucide-react'

const SUGGESTIONS = [
  'What are the main entry points?',
  'Show me the most complex symbols',
  'Which packages have circular dependencies?',
  'Find all HTTP handlers',
  'What does the resolver do?',
  'How are communities detected?',
]

export default function ChatPage() {
  return (
    <div className="flex h-full flex-col">
      <div className="mb-6">
        <h1 className="text-xl font-semibold text-zinc-100">AI Chat</h1>
        <p className="text-sm text-zinc-500">
          Chat with AI about your codebase using Gortex context
        </p>
      </div>

      <div className="flex flex-1 flex-col items-center justify-center">
        <div className="flex max-w-lg flex-col items-center gap-6">
          {/* Icon */}
          <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-gradient-to-br from-zinc-800 to-zinc-900 ring-1 ring-zinc-700/50">
            <MessageSquare className="h-7 w-7 text-zinc-400" />
          </div>

          {/* Title */}
          <div className="text-center">
            <h2 className="text-lg font-medium text-zinc-200">
              Codebase Chat
            </h2>
            <p className="mt-1 text-sm text-zinc-500">
              Ask questions about your code using Gortex&apos;s knowledge graph
              for context-aware answers.
            </p>
          </div>

          {/* Status */}
          <div className="flex items-center gap-2 rounded-full border border-zinc-800 bg-zinc-900/80 px-4 py-2">
            <Sparkles className="h-3.5 w-3.5 text-yellow-500/70" />
            <span className="text-xs text-zinc-400">
              Coming soon
            </span>
          </div>

          {/* Sample questions */}
          <div className="w-full pt-2">
            <p className="mb-3 text-center text-xs font-medium uppercase tracking-wider text-zinc-600">
              Example questions
            </p>
            <div className="grid gap-2 sm:grid-cols-2">
              {SUGGESTIONS.map((q) => (
                <div
                  key={q}
                  className="group flex items-center gap-2 rounded-lg border border-zinc-800/60 bg-zinc-900/50 px-3 py-2.5 text-xs text-zinc-500 transition-colors"
                >
                  <ArrowRight className="h-3 w-3 shrink-0 text-zinc-700 transition-colors group-hover:text-zinc-500" />
                  <span>{q}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
