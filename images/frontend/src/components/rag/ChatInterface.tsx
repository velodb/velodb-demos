import { useState, useRef, useEffect, useCallback, Fragment } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { MessageSquare, Send, Loader2, FileText, Sparkles, User, Bot, Image as ImageIcon, Table2, Calculator, Link, X, Zap, Network, Search } from "lucide-react";
import { cn, getAssetUrl, MultimodalData, RAG_API_BASE_URL as API_BASE_URL } from "@/lib/utils";

// Retrieval method types
type RetrievalMethod = "keyword" | "vector" | "hybrid" | "hybrid+subgraph";

// Method configuration for display
const RETRIEVAL_METHODS: { value: RetrievalMethod; label: string; icon: typeof Search; description: string; color: string }[] = [
  { value: "keyword", label: "Keyword", icon: FileText, description: "BM25 exact match", color: "text-amber-600" },
  { value: "vector", label: "Vector", icon: Sparkles, description: "Semantic search", color: "text-purple-600" },
  { value: "hybrid", label: "Hybrid", icon: Zap, description: "Best of both", color: "text-cyan-600" },
  { value: "hybrid+subgraph", label: "Hybrid+KG", icon: Network, description: "With knowledge graph", color: "text-green-600" },
];

// Source location for highlighting in document viewer
interface SourceLocation {
  page_number: number | null;
  bbox: number[] | null;
  char_start: number | null;
  char_end: number | null;
  source_path: string | null;
}

// Surrounding context for NotebookLM-style viewing
interface SurroundingContext {
  before: string[];
  after: string[];
}

// Citation item from API
interface CitationItem {
  ref_id: string;
  type: "text" | "image" | "table" | "formula";
  chunk_id: string;
  original_markdown: string;
  text_preview: string | null;
  image_path: string | null;
  image_caption: string | null;
  table_markdown: string | null;
  latex: string | null;
  score: number;
  document_name: string | null;
  chunk_position: number | null;
  source_location: SourceLocation | null;
  surrounding_context: SurroundingContext | null;
  // New Lance asset fields
  multimodal_data?: MultimodalData;
}

// API Response
interface CitedQueryResponse {
  query: string;
  answer: string;
  citations: CitationItem[];
  processing_time_ms: number;
}

// Message interface
interface Message {
  id: string;
  role: "user" | "assistant";
  content: string;
  citations?: CitationItem[];
  timestamp: Date;
  method?: string;  // Retrieval method used for this message
}

// Stream cited query from API for faster perceived latency
const streamCitedQuery = async (
  query: string,
  tenantId: string,
  corpusId: string,
  method: RetrievalMethod,
  onToken: (token: string) => void,
  onCitations: (citations: CitationItem[]) => void,
  onDone: (processingTimeMs: number, usedMethod: string) => void,
  onError: (error: string) => void
): Promise<void> => {
  const response = await fetch(`${API_BASE_URL}/api/v1/query/cited/stream`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      query,
      tenant_id: tenantId,
      corpus_id: corpusId,
      top_k: 5,  // Reduced for faster response
      method: method,  // Add retrieval method selection
      explain_method: true,  // Ask LLM to explain how the method helped
      include_surrounding_context: true,
      context_chunks_before: 1,
      context_chunks_after: 1,
    }),
  });

  if (!response.ok) {
    throw new Error(`API error: ${response.status}`);
  }

  const reader = response.body?.getReader();
  if (!reader) throw new Error("No response body");

  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });

    // Process complete SSE messages
    const lines = buffer.split("\n");
    buffer = lines.pop() || ""; // Keep incomplete line in buffer

    for (const line of lines) {
      if (line.startsWith("data: ")) {
        try {
          const data = JSON.parse(line.slice(6));
          if (data.type === "token") {
            onToken(data.content);
          } else if (data.type === "citations") {
            onCitations(data.citations);
          } else if (data.type === "done") {
            onDone(data.processing_time_ms, data.method || "hybrid");
          } else if (data.type === "error") {
            onError(data.message);
          }
        } catch {
          // Ignore parse errors for incomplete JSON
        }
      }
    }
  }
};

// Citation type colors and icons
const citationConfig: Record<string, { color: string; bgColor: string; icon: typeof FileText }> = {
  text: { color: "text-blue-700", bgColor: "bg-blue-100", icon: FileText },
  image: { color: "text-purple-700", bgColor: "bg-purple-100", icon: ImageIcon },
  table: { color: "text-green-700", bgColor: "bg-green-100", icon: Table2 },
  formula: { color: "text-orange-700", bgColor: "bg-orange-100", icon: Calculator },
};

// Citation badge component for text/table/formula
const CitationBadge = ({
  refId,
  citation,
  onClick,
}: {
  refId: string;
  citation: CitationItem | undefined;
  onClick: (citation: CitationItem) => void;
}) => {
  if (!citation) return <span>{refId}</span>;

  const config = citationConfig[citation.type] || citationConfig.text;
  const Icon = config.icon;

  return (
    <button
      onClick={() => onClick(citation)}
      className={cn(
        "inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-xs font-semibold mx-0.5 cursor-pointer hover:opacity-80 transition-opacity align-middle",
        config.bgColor,
        config.color
      )}
      title={`Click to view source: ${citation.document_name || "Document"}`}
    >
      <Icon className="w-3 h-3" />
      {refId}
    </button>
  );
};

// Inline image component - renders image directly with details button
const InlineImageCitation = ({
  refId,
  citation,
  onClick,
  tenantId,
  corpusId,
}: {
  refId: string;
  citation: CitationItem;
  onClick: (citation: CitationItem) => void;
  tenantId?: string;
  corpusId?: string;
}) => {
  const [imageError, setImageError] = useState(false);
  const [isHovered, setIsHovered] = useState(false);

  const imageUrl = getAssetUrl(
    citation.multimodal_data || citation.image_path,
    tenantId || "",
    corpusId || ""
  );

  if (imageError) {
    // Fallback to badge if image fails to load
    return (
      <CitationBadge refId={refId} citation={citation} onClick={onClick} />
    );
  }

  return (
    <figure
      className="my-4 mx-auto max-w-[90%]"
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
    >
      {/* Image container with elegant styling */}
      <div className="relative group rounded-xl overflow-hidden bg-gradient-to-b from-slate-50 to-slate-100 dark:from-slate-800 dark:to-slate-900 p-3 shadow-[0_2px_8px_rgba(0,0,0,0.08)] border border-slate-200/60 dark:border-slate-700/60">
        <img
          src={imageUrl}
          alt={citation.image_caption || citation.multimodal_data?.caption || "Image"}
          className="w-full rounded-lg object-contain max-h-[350px]"
          onError={() => setImageError(true)}
        />
        {/* Subtle overlay button - appears on hover */}
        <button
          onClick={() => onClick(citation)}
          className={cn(
            "absolute top-4 right-4 flex items-center gap-1.5 px-2.5 py-1.5 rounded-full text-xs font-medium transition-all duration-200",
            "bg-white/90 dark:bg-slate-800/90 backdrop-blur-sm",
            "text-slate-600 dark:text-slate-300",
            "border border-slate-200/80 dark:border-slate-600/80",
            "shadow-sm hover:shadow-md",
            "hover:bg-white dark:hover:bg-slate-800",
            isHovered ? "opacity-100 translate-y-0" : "opacity-70 translate-y-0"
          )}
          title="View source details"
        >
          <ImageIcon className="w-3.5 h-3.5 text-purple-500" />
          <span className="text-purple-600 dark:text-purple-400 font-semibold">{refId}</span>
        </button>
      </div>
      {/* Caption with refined styling */}
      {(citation.image_caption || citation.multimodal_data?.caption) && (
        <figcaption className="mt-2 text-center">
          <span className="text-xs text-slate-500 dark:text-slate-400 italic leading-relaxed">
            {citation.image_caption || citation.multimodal_data?.caption}
          </span>
        </figcaption>
      )}
    </figure>
  );
};

// Render markdown with inline citations
const MarkdownWithCitations = ({
  content,
  citations,
  onCitationClick,
  tenantId,
  corpusId,
}: {
  content: string;
  citations: CitationItem[];
  onCitationClick: (citation: CitationItem) => void;
  tenantId?: string;
  corpusId?: string;
}) => {
  // Build citation map for quick lookup - key by ref_id (1, IMG-1, TABLE-1, EQ-1)
  const citationMap = new Map<string, CitationItem>();
  citations.forEach((c) => {
    // Strip brackets if present (handles [1] -> 1, or IMG-1 -> IMG-1)
    const key = c.ref_id.replace(/^\[|\]$/g, "");
    citationMap.set(key, c);
  });

  // Process text nodes to replace citation patterns with badges/images
  const processText = (text: string): React.ReactNode[] => {
    const parts: React.ReactNode[] = [];

    // IMPORTANT: Create regex inside function to avoid lastIndex state issues with global flag
    // Pattern 1: Reference-style markdown images: ![alt text][IMG-X] or ![alt][1]
    const refStyleImagePattern = /!\[([^\]]*)\]\[((?:IMG-\d+)|(?:\d+))\]/g;
    // Pattern 2: Standard citations: [1], [IMG-1], [TABLE-1], [EQ-1]
    const citationPattern = /\[((?:\d+)|(?:IMG-\d+)|(?:TABLE-\d+)|(?:EQ-\d+))\]/g;

    // Track all matches with their positions
    interface MatchInfo {
      start: number;
      end: number;
      type: 'refImage' | 'citation';
      refId: string;
      altText?: string;
    }

    const matches: MatchInfo[] = [];

    // Find reference-style images first (they take precedence)
    let match;
    while ((match = refStyleImagePattern.exec(text)) !== null) {
      matches.push({
        start: match.index,
        end: match.index + match[0].length,
        type: 'refImage',
        refId: match[2], // The reference part (IMG-1, 1, etc.)
        altText: match[1], // The alt text
      });
    }

    // Find standard citations, but skip any that overlap with ref-style images
    while ((match = citationPattern.exec(text)) !== null) {
      const overlaps = matches.some(
        m => match.index >= m.start && match.index < m.end
      );
      if (!overlaps) {
        matches.push({
          start: match.index,
          end: match.index + match[0].length,
          type: 'citation',
          refId: match[1],
        });
      }
    }

    // Sort by position
    matches.sort((a, b) => a.start - b.start);

    // Build result
    let lastIndex = 0;
    for (const m of matches) {
      // Add text before this match
      if (m.start > lastIndex) {
        parts.push(text.slice(lastIndex, m.start));
      }

      const citation = citationMap.get(m.refId);
      const displayRefId = `[${m.refId}]`;

      if (m.type === 'refImage') {
        // Reference-style image: ![alt][IMG-X] -> render as inline image
        if (citation && citation.type === "image" && (citation.image_path || citation.multimodal_data)) {
          parts.push(
            <InlineImageCitation
              key={`img-${m.start}`}
              refId={displayRefId}
              citation={citation}
              onClick={onCitationClick}
              tenantId={tenantId}
              corpusId={corpusId}
            />
          );
        } else {
          // No matching image citation - show as text with badge
          parts.push(
            <span key={`img-fallback-${m.start}`}>
              {m.altText ? `${m.altText} ` : ''}
              <CitationBadge
                refId={displayRefId}
                citation={citation}
                onClick={onCitationClick}
              />
            </span>
          );
        }
      } else {
        // Standard citation: [IMG-1], [1], etc.
        if (citation && citation.type === "image" && (citation.image_path || citation.multimodal_data)) {
          parts.push(
            <InlineImageCitation
              key={`cite-${m.start}`}
              refId={displayRefId}
              citation={citation}
              onClick={onCitationClick}
              tenantId={tenantId}
              corpusId={corpusId}
            />
          );
        } else {
          parts.push(
            <CitationBadge
              key={`cite-${m.start}`}
              refId={displayRefId}
              citation={citation}
              onClick={onCitationClick}
            />
          );
        }
      }

      lastIndex = m.end;
    }

    // Add remaining text
    if (lastIndex < text.length) {
      parts.push(text.slice(lastIndex));
    }

    return parts.length > 0 ? parts : [text];
  };

  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        // Custom text renderer to inject citation badges
        p: ({ children }) => (
          <p className="mb-3 last:mb-0">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </p>
        ),
        // Style headings - also process citations
        h1: ({ children }) => (
          <h1 className="text-lg font-bold mb-2 mt-4 first:mt-0">
            {typeof children === "string" ? processText(children) : children}
          </h1>
        ),
        h2: ({ children }) => (
          <h2 className="text-base font-bold mb-2 mt-3">
            {typeof children === "string" ? processText(children) : children}
          </h2>
        ),
        h3: ({ children }) => (
          <h3 className="text-sm font-bold mb-1 mt-2">
            {typeof children === "string" ? processText(children) : children}
          </h3>
        ),
        // Style lists
        ul: ({ children }) => <ul className="list-disc list-inside mb-3 space-y-1">{children}</ul>,
        ol: ({ children }) => <ol className="list-decimal list-inside mb-3 space-y-1">{children}</ol>,
        li: ({ children }) => (
          <li className="ml-2">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </li>
        ),
        // Style code blocks - process citations inside inline code (LLM sometimes wraps citations in backticks)
        code: ({ className, children }) => {
          const isInline = !className;

          if (isInline && typeof children === "string") {
            // Check if this is just a citation wrapped in backticks like `[1]` or `[IMG-1]`
            const citationMatch = children.match(/^\[((?:\d+)|(?:IMG-\d+)|(?:TABLE-\d+)|(?:EQ-\d+))\]$/);
            if (citationMatch) {
              const refId = citationMatch[1];
              const citation = citationMap.get(refId);
              const displayRefId = `[${refId}]`;

              if (citation && citation.type === "image" && (citation.image_path || citation.multimodal_data)) {
                return (
                  <InlineImageCitation
                    refId={displayRefId}
                    citation={citation}
                    onClick={onCitationClick}
                    tenantId={tenantId}
                    corpusId={corpusId}
                  />
                );
              }
              return (
                <CitationBadge
                  refId={displayRefId}
                  citation={citation}
                  onClick={onCitationClick}
                />
              );
            }
          }

          return isInline ? (
            <code className="bg-slate-200 dark:bg-slate-700 text-slate-800 dark:text-slate-200 px-1.5 py-0.5 rounded text-sm font-mono break-words">{children}</code>
          ) : (
            <code className="block text-sm font-mono whitespace-pre-wrap break-words text-slate-800 dark:text-slate-200">
              {children}
            </code>
          );
        },
        // Handle pre blocks which wrap code - properly constrain width and wrap text
        pre: ({ children }) => (
          <pre className="mb-3 p-3 bg-slate-100 dark:bg-slate-800 rounded-lg max-w-full border border-slate-200 dark:border-slate-700 overflow-hidden">
            <div className="whitespace-pre-wrap break-words">
              {children}
            </div>
          </pre>
        ),
        // Style blockquotes
        blockquote: ({ children }) => (
          <blockquote className="border-l-4 border-primary/30 pl-4 italic my-3">{children}</blockquote>
        ),
        // Style tables - ensure proper overflow handling
        table: ({ children }) => (
          <div className="overflow-x-auto mb-3 max-w-full">
            <table className="min-w-full border-collapse border border-border text-sm">{children}</table>
          </div>
        ),
        th: ({ children }) => (
          <th className="border border-border bg-muted px-3 py-2 text-left font-semibold">
            {typeof children === "string" ? processText(children) : children}
          </th>
        ),
        td: ({ children }) => (
          <td className="border border-border px-3 py-2">
            {typeof children === "string" ? processText(children) : children}
          </td>
        ),
        // Style strong/emphasis - MUST process citations inside bold/italic text
        strong: ({ children }) => (
          <strong className="font-semibold">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </strong>
        ),
        em: ({ children }) => (
          <em className="italic">
            {Array.isArray(children)
              ? children.map((child, i) =>
                  typeof child === "string" ? (
                    <Fragment key={i}>{processText(child)}</Fragment>
                  ) : (
                    <Fragment key={i}>{child}</Fragment>
                  )
                )
              : typeof children === "string"
              ? processText(children)
              : children}
          </em>
        ),
        // Handle images - intercept broken local paths and try to match with citations
        img: ({ src, alt }) => {
          // Check if this is a local file path (broken in browser)
          const isLocalPath = src?.startsWith('/') || src?.startsWith('./') || src?.startsWith('../') ||
                              src?.includes('/Users/') || src?.includes('\\') || !src?.startsWith('http');

          if (isLocalPath && alt) {
            // Try to find a matching image citation by alt text or caption
            const matchingCitation = citations.find(c =>
              c.type === "image" && (
                c.image_caption?.toLowerCase().includes(alt.toLowerCase()) ||
                alt.toLowerCase().includes(c.image_caption?.toLowerCase() || '') ||
                c.original_markdown?.toLowerCase().includes(alt.toLowerCase())
              )
            );

            if (matchingCitation && (matchingCitation.image_path || matchingCitation.multimodal_data)) {
              // Render the matching citation's image
              return (
                <InlineImageCitation
                  refId={`[${matchingCitation.ref_id}]`}
                  citation={matchingCitation}
                  onClick={onCitationClick}
                  tenantId={tenantId}
                  corpusId={corpusId}
                />
              );
            }

            // No matching citation - show placeholder with alt text
            return (
              <span className="inline-flex items-center gap-1 px-2 py-1 bg-slate-100 dark:bg-slate-800 rounded text-sm text-slate-500 dark:text-slate-400 italic">
                <span className="text-purple-500">🖼️</span>
                {alt}
                <span className="text-xs opacity-60">(image not available)</span>
              </span>
            );
          }

          // Valid URL - render normally
          return (
            <img
              src={src}
              alt={alt || "Image"}
              className="max-w-full rounded-lg my-2"
              onError={(e) => {
                const img = e.target as HTMLImageElement;
                img.style.display = "none";
              }}
            />
          );
        },
      }}
    >
      {content}
    </ReactMarkdown>
  );
};

interface ChatInterfaceProps {
  onSendMessage?: (message: string) => void;
  tenantId?: string;
  corpusId?: string;
}

const ChatInterface = ({
  onSendMessage,
  tenantId = "VeloDB Sample",
  corpusId = "velodb_docs"
}: ChatInterfaceProps) => {
  const [messages, setMessages] = useState<Message[]>([]);
  const [inputValue, setInputValue] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [streamingContent, setStreamingContent] = useState("");
  const [selectedCitation, setSelectedCitation] = useState<CitationItem | null>(null);
  const [selectedMethod, setSelectedMethod] = useState<RetrievalMethod>("hybrid");
  const [lastUserQuery, setLastUserQuery] = useState<string | null>(null);  // Track last query for auto-re-retrieval
  const scrollAreaRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // Auto-scroll to bottom when new messages arrive or streaming updates
  useEffect(() => {
    if (scrollAreaRef.current) {
      const scrollContainer = scrollAreaRef.current.querySelector("[data-radix-scroll-area-viewport]");
      if (scrollContainer) {
        scrollContainer.scrollTop = scrollContainer.scrollHeight;
      }
    }
  }, [messages, streamingContent]);

  // Generate unique ID
  const generateId = () => `msg_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`;

  const handleCitationClick = useCallback((citation: CitationItem) => {
    setSelectedCitation(citation);
  }, []);

  // Core function to execute a query with the current method
  const executeQuery = useCallback(async (query: string, isMethodSwitch: boolean = false) => {
    if (!query || isLoading) return;

    // Clear streaming content
    setStreamingContent("");

    // For method switch, add a system message instead of user message
    if (isMethodSwitch) {
      const methodConfig = getMethodConfig(selectedMethod);
      const switchMessage: Message = {
        id: generateId(),
        role: "user",
        content: `🔄 Re-running with ${methodConfig.label}: "${query}"`,
        timestamp: new Date(),
      };
      setMessages((prev) => [...prev, switchMessage]);
    } else {
      // Add user message for normal query
      const userMessage: Message = {
        id: generateId(),
        role: "user",
        content: query,
        timestamp: new Date(),
      };
      setMessages((prev) => [...prev, userMessage]);
      setLastUserQuery(query);  // Track the query for auto-re-retrieval
      onSendMessage?.(query);
    }

    // Start loading
    setIsLoading(true);

    // Track streaming content
    let fullContent = "";
    let receivedCitations: CitationItem[] = [];
    let usedMethod = selectedMethod;

    try {
      // Use streaming API for faster perceived latency
      await streamCitedQuery(
        query,
        tenantId,
        corpusId,
        selectedMethod,  // Pass selected retrieval method
        // onToken - update streaming content in real-time
        (token) => {
          fullContent += token;
          setStreamingContent(fullContent);
        },
        // onCitations - store citations when received
        (citations) => {
          receivedCitations = citations;
        },
        // onDone - finalize message with citations and method
        (_processingTime, method) => {
          usedMethod = method as RetrievalMethod;
          // Add final assistant message with citations and method
          const assistantMessage: Message = {
            id: generateId(),
            role: "assistant",
            content: fullContent,
            citations: receivedCitations,
            timestamp: new Date(),
            method: usedMethod,  // Store the method used
          };
          setMessages((prev) => [...prev, assistantMessage]);
          setStreamingContent("");
          setIsLoading(false);
          inputRef.current?.focus();
        },
        // onError
        (error) => {
          console.error("Stream error:", error);
          const errorMessage: Message = {
            id: generateId(),
            role: "assistant",
            content: `Error: ${error}`,
            timestamp: new Date(),
          };
          setMessages((prev) => [...prev, errorMessage]);
          setStreamingContent("");
          setIsLoading(false);
        }
      );
    } catch (error) {
      console.error("API error:", error);
      // Add error message
      const errorMessage: Message = {
        id: generateId(),
        role: "assistant",
        content: "Sorry, I encountered an error processing your request. Please try again.",
        timestamp: new Date(),
      };
      setMessages((prev) => [...prev, errorMessage]);
    } finally {
      setIsLoading(false);
      // Focus input after response
      inputRef.current?.focus();
    }
  }, [isLoading, onSendMessage, tenantId, corpusId, selectedMethod]);

  // Wrapper for sending new queries from input
  const handleSend = useCallback(async () => {
    const query = inputValue.trim();
    if (!query) return;
    setInputValue("");
    await executeQuery(query, false);
  }, [inputValue, executeQuery]);

  // Auto-re-retrieve when method changes (if there's a previous query)
  useEffect(() => {
    if (lastUserQuery && messages.length > 0) {
      // Small delay to let the UI update the dropdown first
      const timer = setTimeout(() => {
        executeQuery(lastUserQuery, true);
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [selectedMethod]);  // Only trigger on method change, not on executeQuery change

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const formatTime = (date: Date) => {
    return date.toLocaleTimeString("en-US", {
      hour: "2-digit",
      minute: "2-digit",
    });
  };

  // Get method config for display
  const getMethodConfig = (method: string) => {
    return RETRIEVAL_METHODS.find(m => m.value === method) || RETRIEVAL_METHODS[2]; // default to hybrid
  };

  return (
    <>
      <Card className="h-full flex flex-col">
        <CardHeader className="pb-4 flex-shrink-0">
          <div className="flex items-start justify-between gap-4">
            <div className="flex-1">
              <CardTitle className="flex items-center gap-2">
                <MessageSquare className="w-5 h-5 text-green-600" />
                Chat with Documents
              </CardTitle>
              <p className="text-sm text-muted-foreground mt-1">
                Ask questions and get RAG-powered responses with inline citations
              </p>
            </div>

            {/* Retrieval Method Selector */}
            <div className="flex items-center gap-2">
              <Select
                value={selectedMethod}
                onValueChange={(value) => setSelectedMethod(value as RetrievalMethod)}
              >
                <SelectTrigger className="w-[160px] h-9">
                  <SelectValue>
                    {(() => {
                      const config = getMethodConfig(selectedMethod);
                      const Icon = config.icon;
                      return (
                        <span className="flex items-center gap-2">
                          <Icon className={cn("w-4 h-4", config.color)} />
                          <span>{config.label}</span>
                        </span>
                      );
                    })()}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {RETRIEVAL_METHODS.map((method) => {
                    const Icon = method.icon;
                    return (
                      <SelectItem key={method.value} value={method.value}>
                        <div className="flex items-center gap-2">
                          <Icon className={cn("w-4 h-4", method.color)} />
                          <span className="font-medium">{method.label}</span>
                          <span className="text-xs text-muted-foreground">({method.description})</span>
                        </div>
                      </SelectItem>
                    );
                  })}
                </SelectContent>
              </Select>

              {/* New Chat button - clears conversation for sales to start fresh */}
              {messages.length > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => {
                    setMessages([]);
                    setStreamingContent("");
                    setInputValue("");
                    setLastUserQuery(null);  // Clear last query to prevent auto-re-retrieval
                    inputRef.current?.focus();
                  }}
                  className="flex items-center gap-1.5 text-muted-foreground hover:text-foreground h-9"
                  title="Start a new conversation"
                >
                  <X className="w-4 h-4" />
                  New Chat
                </Button>
              )}
            </div>
          </div>
        </CardHeader>
        <CardContent className="flex-1 flex flex-col min-h-0 space-y-4">
          {/* Messages Area */}
          <ScrollArea className="flex-1 pr-4" ref={scrollAreaRef}>
            <div className="space-y-4 pb-4">
              {messages.length === 0 ? (
                <div className="text-center py-12 text-muted-foreground">
                  <MessageSquare className="w-12 h-12 mx-auto mb-4 opacity-20" />
                  <p className="font-medium">No messages yet</p>
                  <p className="text-sm mt-2">
                    Ask a question about your documents to get started
                  </p>
                  <div className="mt-6 space-y-3">
                    <p className="text-xs text-muted-foreground">
                      Try these queries with different retrieval methods:
                    </p>
                    <div className="flex flex-col gap-2 items-center">
                      {[
                        // Q1: Hybrid > Keyword - BM25 returns irrelevant docs, Hybrid finds Runtime Filter
                        "How does runtime filter help with large table joins?",
                        // Q2: Hybrid > Vector - Vector has generic image at #1, Hybrid finds exact match
                        "RuntimeFilterState = READY",
                        // Q3: Hybrid+KG > Hybrid - KG finds 4 docs vs 1 doc (4x improvement)
                        "When should I use bucket shuffle join instead of colocate join?",
                        // Q4: Hybrid+KG > Hybrid - KG finds 8 docs vs 5 docs (diagnostic + optimization)
                        "How can I diagnose and optimize slow join queries?",
                      ].map((suggestion) => (
                        <Button
                          key={suggestion}
                          variant="outline"
                          size="sm"
                          className="text-xs w-full max-w-md text-left h-auto py-2 px-4"
                          onClick={() => {
                            setInputValue(suggestion);
                            inputRef.current?.focus();
                          }}
                        >
                          {suggestion}
                        </Button>
                      ))}
                    </div>
                    <p className="text-[10px] text-muted-foreground/60">
                      Tip: Switch retrieval methods above to compare results
                    </p>
                  </div>
                </div>
              ) : (
                messages.map((message) => (
                  <div
                    key={message.id}
                    className={cn(
                      "flex gap-3",
                      message.role === "user" ? "justify-end" : "justify-start"
                    )}
                  >
                    {/* Avatar for assistant */}
                    {message.role === "assistant" && (
                      <div className="flex-shrink-0 w-8 h-8 rounded-full bg-gradient-to-br from-green-500 to-emerald-600 flex items-center justify-center">
                        <Bot className="w-4 h-4 text-white" />
                      </div>
                    )}

                    <div
                      className={cn(
                        "max-w-[80%] min-w-0 space-y-2",
                        message.role === "user" ? "items-end" : "items-start"
                      )}
                    >
                      {/* Message Bubble */}
                      <div
                        className={cn(
                          "rounded-lg px-4 py-3 min-w-0 overflow-hidden",
                          message.role === "user"
                            ? "bg-primary text-primary-foreground"
                            : "bg-muted"
                        )}
                      >
                        <div className="text-sm prose prose-sm max-w-none dark:prose-invert overflow-x-auto [&_pre]:max-w-full [&_code]:break-words">
                          {message.role === "assistant" && message.citations ? (
                            <MarkdownWithCitations
                              content={message.content}
                              citations={message.citations}
                              onCitationClick={handleCitationClick}
                              tenantId={tenantId}
                              corpusId={corpusId}
                            />
                          ) : (
                            <span className="whitespace-pre-wrap">{message.content}</span>
                          )}
                        </div>
                      </div>

                      {/* Timestamp and Method Badge */}
                      <div
                        className={cn(
                          "flex items-center gap-2 text-xs text-muted-foreground px-1",
                          message.role === "user" ? "justify-end" : "justify-start"
                        )}
                      >
                        {formatTime(message.timestamp)}
                        {/* Show method badge for assistant messages */}
                        {message.role === "assistant" && message.method && (() => {
                          const config = getMethodConfig(message.method);
                          const Icon = config.icon;
                          return (
                            <span className={cn(
                              "inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs font-medium",
                              "bg-slate-100 dark:bg-slate-800"
                            )}>
                              <Icon className={cn("w-3 h-3", config.color)} />
                              <span className={config.color}>{config.label}</span>
                            </span>
                          );
                        })()}
                      </div>

                      {/* Citation Legend (only for assistant messages with citations) */}
                      {message.role === "assistant" && message.citations && message.citations.length > 0 && (
                        <div className="space-y-2 mt-2">
                          <div className="flex items-center gap-1 text-xs text-muted-foreground">
                            <Link className="w-3 h-3" />
                            <span>Click any citation badge to view source</span>
                          </div>
                          <div className="flex flex-wrap gap-1">
                            {message.citations.map((citation) => {
                              const config = citationConfig[citation.type] || citationConfig.text;
                              const Icon = config.icon;
                              return (
                                <button
                                  key={citation.ref_id}
                                  onClick={() => handleCitationClick(citation)}
                                  className={cn(
                                    "flex items-center gap-1 px-2 py-1 rounded text-xs cursor-pointer hover:opacity-80 transition-opacity border",
                                    config.bgColor,
                                    config.color
                                  )}
                                >
                                  <Icon className="w-3 h-3" />
                                  <span className="font-medium">{citation.ref_id}</span>
                                  <span className="opacity-70 truncate max-w-[100px]">
                                    {citation.document_name || "Source"}
                                  </span>
                                </button>
                              );
                            })}
                          </div>
                        </div>
                      )}
                    </div>

                    {/* Avatar for user */}
                    {message.role === "user" && (
                      <div className="flex-shrink-0 w-8 h-8 rounded-full bg-gradient-to-br from-blue-500 to-indigo-600 flex items-center justify-center">
                        <User className="w-4 h-4 text-white" />
                      </div>
                    )}
                  </div>
                ))
              )}

              {/* Streaming response or loading indicator */}
              {isLoading && (
                <div className="flex gap-3 justify-start">
                  <div className="flex-shrink-0 w-8 h-8 rounded-full bg-gradient-to-br from-green-500 to-emerald-600 flex items-center justify-center">
                    <Bot className="w-4 h-4 text-white" />
                  </div>
                  <div className="max-w-[80%] min-w-0 bg-muted rounded-lg px-4 py-3 overflow-hidden">
                    {streamingContent ? (
                      <div className="text-sm whitespace-pre-wrap break-words overflow-x-auto">
                        {streamingContent}
                        <span className="inline-block w-2 h-4 ml-1 bg-green-500 animate-pulse" />
                      </div>
                    ) : (
                      <div className="flex items-center gap-2">
                        <Loader2 className="w-4 h-4 animate-spin text-muted-foreground" />
                        <span className="text-sm text-muted-foreground">Searching documents...</span>
                      </div>
                    )}
                  </div>
                </div>
              )}
            </div>
          </ScrollArea>

          {/* Input Area */}
          <div className="flex-shrink-0 flex gap-2 pt-2 border-t">
            <Input
              ref={inputRef}
              placeholder="Ask a question about your documents..."
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              onKeyDown={handleKeyDown}
              disabled={isLoading}
              className="flex-1"
            />
            <Button
              onClick={handleSend}
              disabled={isLoading || !inputValue.trim()}
              className="min-w-[80px]"
            >
              {isLoading ? (
                <Loader2 className="w-4 h-4 animate-spin" />
              ) : (
                <>
                  <Send className="w-4 h-4 mr-2" />
                  Send
                </>
              )}
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* Document Viewer Sheet */}
      <Sheet open={!!selectedCitation} onOpenChange={(open) => !open && setSelectedCitation(null)}>
        <SheetContent className="w-[600px] sm:max-w-[600px] overflow-y-auto">
          <SheetHeader className="relative">
            {/* Close button */}
            <button
              onClick={() => setSelectedCitation(null)}
              className="absolute right-0 top-0 p-2 rounded-full hover:bg-muted transition-colors"
              aria-label="Close panel"
            >
              <X className="w-5 h-5 text-muted-foreground hover:text-foreground" />
            </button>
            <SheetTitle className="flex items-center gap-2 pr-10">
              {selectedCitation && (
                <>
                  {(() => {
                    const config = citationConfig[selectedCitation.type] || citationConfig.text;
                    const Icon = config.icon;
                    return <Icon className={cn("w-5 h-5", config.color)} />;
                  })()}
                  <span>Source: {selectedCitation.ref_id}</span>
                </>
              )}
            </SheetTitle>
          </SheetHeader>

          {selectedCitation && (
            <div className="mt-6 space-y-6">
              {/* Document Info */}
              <div className="flex items-center gap-4 text-sm text-muted-foreground">
                <div className="flex items-center gap-1">
                  <FileText className="w-4 h-4" />
                  <span>{selectedCitation.document_name || "Unknown"}</span>
                </div>
                {selectedCitation.source_location?.page_number && (
                  <div>Page {selectedCitation.source_location.page_number}</div>
                )}
                <div>Score: {selectedCitation.score.toFixed(3)}</div>
              </div>

              {/* Surrounding Context - Before */}
              {selectedCitation.surrounding_context?.before && selectedCitation.surrounding_context.before.length > 0 && (
                <div className="space-y-2">
                  <div className="text-xs text-muted-foreground uppercase tracking-wide">Previous Chunks</div>
                  {selectedCitation.surrounding_context.before.map((content, idx) => (
                    <div
                      key={`before-${idx}`}
                      className="p-3 bg-muted/30 rounded-lg text-sm text-muted-foreground border-l-2 border-muted"
                    >
                      <pre className="whitespace-pre-wrap font-sans">{content}</pre>
                    </div>
                  ))}
                </div>
              )}

              {/* Main Cited Content - Highlighted */}
              <div className="space-y-2">
                <div className="text-xs text-muted-foreground uppercase tracking-wide flex items-center gap-2">
                  <Sparkles className="w-3 h-3" />
                  Cited Content
                </div>
                <div
                  className={cn(
                    "p-4 rounded-lg border-2",
                    selectedCitation.type === "text" && "border-blue-300 bg-blue-50",
                    selectedCitation.type === "image" && "border-purple-300 bg-purple-50",
                    selectedCitation.type === "table" && "border-green-300 bg-green-50",
                    selectedCitation.type === "formula" && "border-orange-300 bg-orange-50"
                  )}
                >
                  {/* Image */}
                  {selectedCitation.type === "image" && (selectedCitation.image_path || selectedCitation.multimodal_data) && (
                    <div className="mb-4">
                      <img
                        src={getAssetUrl(
                          selectedCitation.multimodal_data || selectedCitation.image_path,
                          tenantId,
                          corpusId
                        )}
                        alt={selectedCitation.image_caption || selectedCitation.multimodal_data?.caption || "Image"}
                        className="max-w-full rounded-lg shadow-md"
                        onError={(e) => {
                          const img = e.target as HTMLImageElement;
                          img.style.display = "none";
                          // Show fallback message
                          const fallback = document.createElement("div");
                          fallback.className = "p-4 bg-muted rounded-lg text-center text-muted-foreground";
                          fallback.textContent = "Image not available";
                          img.parentNode?.appendChild(fallback);
                        }}
                      />
                      {(selectedCitation.image_caption || selectedCitation.multimodal_data?.caption) && (
                        <p className="mt-2 text-sm text-muted-foreground italic">
                          {selectedCitation.image_caption || selectedCitation.multimodal_data?.caption}
                        </p>
                      )}
                    </div>
                  )}

                  {/* Table - render as actual HTML table */}
                  {selectedCitation.type === "table" && (selectedCitation.table_markdown || selectedCitation.original_markdown) && (
                    <div className="overflow-x-auto">
                      <ReactMarkdown
                        remarkPlugins={[remarkGfm]}
                        components={{
                          table: ({ children }) => (
                            <table className="min-w-full border-collapse border border-slate-300 text-sm">
                              {children}
                            </table>
                          ),
                          thead: ({ children }) => (
                            <thead className="bg-slate-100">{children}</thead>
                          ),
                          th: ({ children }) => (
                            <th className="border border-slate-300 px-3 py-2 text-left font-semibold text-slate-700">
                              {children}
                            </th>
                          ),
                          td: ({ children }) => (
                            <td className="border border-slate-300 px-3 py-2 whitespace-pre-wrap">{children}</td>
                          ),
                          tr: ({ children }) => (
                            <tr className="hover:bg-slate-50">{children}</tr>
                          ),
                          // Fallback for non-table content (like EXPLAIN output)
                          p: ({ children }) => (
                            <pre className="whitespace-pre-wrap font-mono text-xs bg-slate-50 p-3 rounded overflow-x-auto">{children}</pre>
                          ),
                        }}
                      >
                        {(selectedCitation.table_markdown || selectedCitation.original_markdown || "").replace(/\\n/g, '\n').replace(/\\t/g, '\t')}
                      </ReactMarkdown>
                    </div>
                  )}

                  {/* Formula */}
                  {selectedCitation.type === "formula" && selectedCitation.latex && (
                    <div className="mb-4 p-4 bg-white rounded text-center font-mono">
                      {selectedCitation.latex}
                    </div>
                  )}

                  {/* Main Text Content - only show for text type or if no specialized rendering */}
                  {(selectedCitation.type === "text" ||
                    (selectedCitation.type !== "image" && selectedCitation.type !== "table" && selectedCitation.type !== "formula")) && (
                    <div className="prose prose-sm max-w-none">
                      <ReactMarkdown remarkPlugins={[remarkGfm]}>
                        {selectedCitation.original_markdown || selectedCitation.text_preview || ""}
                      </ReactMarkdown>
                    </div>
                  )}
                </div>
              </div>

              {/* Surrounding Context - After */}
              {selectedCitation.surrounding_context?.after && selectedCitation.surrounding_context.after.length > 0 && (
                <div className="space-y-2">
                  <div className="text-xs text-muted-foreground uppercase tracking-wide">Following Chunks</div>
                  {selectedCitation.surrounding_context.after.map((content, idx) => (
                    <div
                      key={`after-${idx}`}
                      className="p-3 bg-muted/30 rounded-lg text-sm text-muted-foreground border-l-2 border-muted"
                    >
                      <pre className="whitespace-pre-wrap font-sans">{content}</pre>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </SheetContent>
      </Sheet>
    </>
  );
};

export default ChatInterface;
