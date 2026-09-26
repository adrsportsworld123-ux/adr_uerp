"use client";

// Phase 6 v1 continued — NLP-BI (POST /ai/ask) and the AI Copilot
// (POST /ai/copilot/chat), see erp-core-go's internal/ai/nlpbi.go and
// copilot.go. Both endpoints answer 503 LLM_UNAVAILABLE until an operator
// sets LLM_PROVIDER (ollama or anthropic) — this page shows that message
// plainly rather than pretending the feature is broken.
import { useState } from "react";
import { api, ApiError } from "@/lib/api-client";
import { AskResponse, ChartPoint, CopilotMessage } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

// A small dependency-free bar chart — this codebase has no charting
// library yet, and the data here (at most a couple dozen points) doesn't
// justify adding one. Bars are scaled to the largest value in the series.
function SimpleBarChart({ series }: { series: ChartPoint[] }) {
  const max = Math.max(1, ...series.map((s) => Number(s.value) || 0));
  return (
    <div className="flex flex-col gap-2">
      {series.map((s) => {
        const pct = Math.max(2, (100 * (Number(s.value) || 0)) / max);
        return (
          <div key={s.label} className="flex items-center gap-2 text-sm">
            <div className="w-40 truncate text-zinc-600" title={s.label}>{s.label}</div>
            <div className="flex-1 bg-zinc-100 rounded h-5 overflow-hidden">
              <div className="bg-blue-500 h-full rounded" style={{ width: `${pct}%` }} />
            </div>
            <div className="w-24 text-right font-medium">{s.value}</div>
          </div>
        );
      })}
      {series.length === 0 && <p className="text-sm text-zinc-500">No data to chart.</p>}
    </div>
  );
}

function AskAIPanel() {
  const [question, setQuestion] = useState("");
  const [asking, setAsking] = useState(false);
  const [result, setResult] = useState<AskResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function handleAsk() {
    if (!question.trim()) return;
    setAsking(true);
    setError(null);
    try {
      const data = await api.post<AskResponse>("/api/v1/ai/ask", { question });
      setResult(data);
    } catch (e) {
      setResult(null);
      setError(e instanceof ApiError ? e.message : "Could not get an answer");
    } finally {
      setAsking(false);
    }
  }

  const examples = [
    "What were today's sales at MG Road?",
    "Show me the stock summary for MG Road",
    "What's today's consolidated sales across all branches?",
    "How much cash did we collect today at MG Road?",
  ];

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Ask AI</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-xs text-zinc-500">
          Ask a question about today&apos;s (or another day&apos;s) sales, stock, or cash — daily sales, stock
          summary, EOD cash, consolidated sales, or consolidated stock. Anything else is politely declined rather
          than guessed at.
        </p>
        <div className="flex flex-wrap gap-2">
          {examples.map((ex) => (
            <button
              key={ex}
              type="button"
              onClick={() => setQuestion(ex)}
              className="text-xs text-blue-600 border border-blue-200 rounded-full px-3 py-1 hover:bg-blue-50"
            >
              {ex}
            </button>
          ))}
        </div>
        <div className="flex gap-2">
          <Input
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder="Ask a question about your sales or stock..."
            onKeyDown={(e) => e.key === "Enter" && handleAsk()}
          />
          <Button onClick={handleAsk} disabled={asking || !question.trim()}>
            {asking ? "Asking..." : "Ask"}
          </Button>
        </div>

        {error && <p className="text-sm text-red-600">{error}</p>}

        {result && (
          <div className="flex flex-col gap-3 border-t pt-4">
            <p className="text-sm">{result.answer || "(no summary available)"}</p>
            {result.chart.type === "stat" ? (
              <div className="text-2xl font-semibold">{result.chart.value}</div>
            ) : (
              <SimpleBarChart series={result.chart.series ?? []} />
            )}
            <details className="text-xs text-zinc-500">
              <summary className="cursor-pointer">Raw data ({result.intent})</summary>
              <pre className="whitespace-pre-wrap break-all mt-2">{JSON.stringify(result.data, null, 2)}</pre>
            </details>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function CopilotPanel() {
  const [messages, setMessages] = useState<CopilotMessage[]>([]);
  const [input, setInput] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSend() {
    if (!input.trim()) return;
    const next = [...messages, { role: "user" as const, content: input.trim() }];
    setMessages(next);
    setInput("");
    setSending(true);
    setError(null);
    try {
      const data = await api.post<{ reply: string }>("/api/v1/ai/copilot/chat", { messages: next });
      setMessages([...next, { role: "assistant" as const, content: data.reply }]);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the copilot");
    } finally {
      setSending(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">AI Copilot</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <p className="text-xs text-zinc-500">
          Advisory only — the copilot can discuss today&apos;s numbers and give general operations advice, but it
          cannot place orders, change prices, adjust stock, or take any action in this system.
        </p>
        <div className="flex flex-col gap-3 max-h-80 overflow-y-auto border rounded p-3 bg-zinc-50">
          {messages.length === 0 && <p className="text-sm text-zinc-400">Ask the copilot something, e.g. &quot;How are we doing today?&quot;</p>}
          {messages.map((m, i) => (
            <div key={i} className={m.role === "user" ? "text-right" : "text-left"}>
              <span
                className={
                  "inline-block rounded-lg px-3 py-2 text-sm max-w-[80%] " +
                  (m.role === "user" ? "bg-blue-600 text-white" : "bg-white border text-zinc-800")
                }
              >
                {m.content}
              </span>
            </div>
          ))}
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <div className="flex gap-2">
          <Textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Ask the copilot for advice..."
            rows={2}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                handleSend();
              }
            }}
          />
          <Button onClick={handleSend} disabled={sending || !input.trim()}>
            {sending ? "Sending..." : "Send"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

export default function AIAssistantPage() {
  return (
    <div className="flex flex-col gap-6 max-w-4xl">
      <h1 className="text-2xl font-semibold">Ask AI / Copilot</h1>
      <p className="text-sm text-zinc-500">
        Both panels below go through a configurable LLM backend (a self-hosted Ollama server or the Anthropic API —
        see <code>LLM_PROVIDER</code>). If neither is configured yet, both will show a plain &quot;LLM isn&apos;t
        configured&quot; error rather than failing silently.
      </p>
      <AskAIPanel />
      <CopilotPanel />
    </div>
  );
}
