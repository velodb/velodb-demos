import { useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Copy, Check, Database } from "lucide-react";
import { cn } from "@/lib/utils";

export interface SQLQuery {
  id: string;
  label: string;
  sql: string;
  description?: string;
  executionTime?: number; // in milliseconds
}

interface SQLPreviewPanelProps {
  queries: SQLQuery[];
  title?: string;
  className?: string;
  onCopy?: (query: SQLQuery) => void;
}

// SQL syntax highlighting with simple regex-based approach
const highlightSQL = (sql: string): React.ReactNode => {
  // Keywords to highlight
  const keywords = [
    "SELECT", "FROM", "WHERE", "AND", "OR", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER",
    "ON", "GROUP BY", "ORDER BY", "LIMIT", "OFFSET", "AS", "DISTINCT", "COUNT",
    "SUM", "AVG", "MAX", "MIN", "HAVING", "IN", "NOT", "NULL", "IS", "LIKE",
    "INSERT", "UPDATE", "DELETE", "CREATE", "TABLE", "INDEX", "VIEW", "DROP",
    "ALTER", "UNION", "ALL", "CASE", "WHEN", "THEN", "ELSE", "END", "WITH",
    "USING", "EXISTS", "BETWEEN", "ASC", "DESC", "VECTOR_SEARCH", "BM25_SEARCH",
    "COSINE_SIMILARITY", "ARRAY", "MATCH_AGAINST"
  ];

  // Split SQL into tokens while preserving whitespace and structure
  const tokens = sql.split(/(\s+|[(),;])/);

  return tokens.map((token, idx) => {
    const upperToken = token.toUpperCase();

    // SQL keywords (blue)
    if (keywords.includes(upperToken)) {
      return (
        <span key={idx} className="text-blue-600 font-semibold">
          {token}
        </span>
      );
    }

    // String literals (green)
    if (token.match(/^'.*'$/)) {
      return (
        <span key={idx} className="text-green-600">
          {token}
        </span>
      );
    }

    // Numbers (orange)
    if (token.match(/^\d+(\.\d+)?$/)) {
      return (
        <span key={idx} className="text-orange-600">
          {token}
        </span>
      );
    }

    // Comments (gray)
    if (token.startsWith("--")) {
      return (
        <span key={idx} className="text-gray-500 italic">
          {token}
        </span>
      );
    }

    // Function names (purple) - detect pattern like FUNCTION_NAME(
    if (idx < tokens.length - 1 && tokens[idx + 1] === "(") {
      return (
        <span key={idx} className="text-purple-600 font-medium">
          {token}
        </span>
      );
    }

    return <span key={idx}>{token}</span>;
  });
};

const SQLPreviewPanel = ({
  queries,
  title = "SQL Queries",
  className,
  onCopy,
}: SQLPreviewPanelProps) => {
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const handleCopy = async (query: SQLQuery) => {
    try {
      await navigator.clipboard.writeText(query.sql);
      setCopiedId(query.id);
      onCopy?.(query);

      // Reset after 2 seconds
      setTimeout(() => {
        setCopiedId(null);
      }, 2000);
    } catch (err) {
      console.error("Failed to copy SQL:", err);
    }
  };

  return (
    <Card className={cn("h-full", className)}>
      <CardHeader className="pb-4">
        <CardTitle className="flex items-center gap-2">
          <Database className="w-5 h-5 text-blue-600" />
          {title}
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {queries.length === 0 ? (
          <div className="text-center py-8 text-muted-foreground">
            <Database className="w-12 h-12 mx-auto mb-3 opacity-30" />
            <p className="text-sm">No queries to display</p>
          </div>
        ) : (
          queries.map((query) => {
            const isCopied = copiedId === query.id;

            return (
              <Card
                key={query.id}
                className="border-l-4 border-l-blue-500"
                data-testid={`sql-query-${query.id}`}
              >
                <CardHeader className="pb-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex-1">
                      <h4 className="font-medium text-sm">{query.label}</h4>
                      {query.description && (
                        <p className="text-xs text-muted-foreground mt-1">
                          {query.description}
                        </p>
                      )}
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleCopy(query)}
                      className={cn(
                        "transition-colors",
                        isCopied && "text-green-600"
                      )}
                      data-testid={`copy-button-${query.id}`}
                    >
                      {isCopied ? (
                        <>
                          <Check className="w-4 h-4 mr-1" />
                          Copied
                        </>
                      ) : (
                        <>
                          <Copy className="w-4 h-4 mr-1" />
                          Copy
                        </>
                      )}
                    </Button>
                  </div>
                </CardHeader>
                <CardContent>
                  {/* SQL Code Block */}
                  <div className="bg-gray-50 rounded-lg p-4 border overflow-x-auto">
                    <pre className="text-xs font-mono leading-relaxed">
                      <code>{highlightSQL(query.sql)}</code>
                    </pre>
                  </div>

                  {/* Execution Time */}
                  {query.executionTime !== undefined && (
                    <div className="mt-2 text-xs text-muted-foreground">
                      Execution time:{" "}
                      <span className="font-mono font-medium text-foreground">
                        {query.executionTime}ms
                      </span>
                    </div>
                  )}
                </CardContent>
              </Card>
            );
          })
        )}

        {/* Info Section */}
        {queries.length > 0 && (
          <div className="p-3 bg-blue-50/50 rounded-lg text-xs">
            <p className="text-muted-foreground">
              <strong>SQL Queries:</strong> These queries show how VeloDB
              retrieves and processes data using SQL with vector search, BM25,
              and hybrid search capabilities.
            </p>
          </div>
        )}
      </CardContent>
    </Card>
  );
};

export default SQLPreviewPanel;
