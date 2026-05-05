import React, { useMemo, useState } from 'react';
import {
    Tooltip,
    TooltipContent,
    TooltipProvider,
    TooltipTrigger,
} from "@/components/ui/tooltip";
import { Badge } from "@/components/ui/badge";
import { Copy, Check } from "lucide-react";

interface QueryTooltipProps {
    children: React.ReactNode;
    query: string;
    latency?: string;
    scanned?: string;
    type?: 'SQL' | 'JSON' | 'KV';
    badge?: string;  // Optional additional badge text
    onBadgeClick?: () => void;  // Optional click handler for badge
}

// SQL syntax highlighting color scheme
const SQL_COLORS = {
    keyword: 'text-blue-400 font-bold',          // SELECT, FROM, WHERE, etc.
    special: 'text-purple-400 font-bold',         // MATERIALIZED VIEW, OVER, WINDOW
    function: 'text-cyan-400',                    // SUM, AVG, COUNT, json_extract
    string: 'text-green-400',                     // 'string literals'
    number: 'text-yellow-400',                    // 123, 0.45
    comment: 'text-gray-500 italic',              // -- comments
    default: 'text-slate-300',                    // Everything else
};

// SQL keywords to highlight
const SQL_KEYWORDS = [
    'SELECT', 'FROM', 'WHERE', 'JOIN', 'LEFT', 'RIGHT', 'INNER', 'OUTER', 'FULL',
    'ON', 'AND', 'OR', 'NOT', 'IN', 'IS', 'NULL', 'AS', 'GROUP', 'BY', 'ORDER',
    'HAVING', 'LIMIT', 'OFFSET', 'UNION', 'ALL', 'DISTINCT', 'INSERT', 'INTO',
    'VALUES', 'UPDATE', 'SET', 'DELETE', 'CREATE', 'TABLE', 'INDEX', 'VIEW',
    'DROP', 'ALTER', 'ADD', 'COLUMN', 'BETWEEN', 'LIKE', 'CASE', 'WHEN', 'THEN',
    'ELSE', 'END', 'WITH', 'ASC', 'DESC', 'USING', 'INTERVAL', 'TRUE', 'FALSE',
];

// Special SQL keywords (VeloDB-specific, window functions)
const SPECIAL_KEYWORDS = [
    'MATERIALIZED', 'OVER', 'WINDOW', 'PARTITION', 'ROWS', 'RANGE', 'PRECEDING',
    'FOLLOWING', 'UNBOUNDED', 'CURRENT', 'ROW', 'REFRESH', 'ASYNC', 'EVERY',
    'DISTRIBUTED', 'HASH', 'BUCKETS', 'DUPLICATE', 'KEY', 'AGGREGATE',
];

// SQL functions
const SQL_FUNCTIONS = [
    'SUM', 'AVG', 'COUNT', 'MIN', 'MAX', 'COALESCE', 'NULLIF', 'IFNULL',
    'CAST', 'CONVERT', 'DATE_FORMAT', 'DATE_SUB', 'DATE_ADD', 'NOW', 'CURDATE',
    'SUBSTRING', 'CONCAT', 'TRIM', 'UPPER', 'LOWER', 'LENGTH', 'REPLACE',
    'LAG', 'LEAD', 'ROW_NUMBER', 'RANK', 'DENSE_RANK', 'FIRST_VALUE', 'LAST_VALUE',
    'PERCENTILE_CONT', 'WITHIN', 'json_extract', 'get_json_string', 'get_json_double',
    'JSON_EXTRACT', 'GET_JSON_STRING', 'GET_JSON_DOUBLE',
];

// Highlight SQL syntax with colors
function highlightSQL(sql: string): React.ReactNode[] {
    const result: React.ReactNode[] = [];
    let remaining = sql;
    let keyIndex = 0;

    while (remaining.length > 0) {
        let matched = false;

        // Check for comments (-- to end of line)
        if (remaining.startsWith('--')) {
            const endOfLine = remaining.indexOf('\n');
            const comment = endOfLine === -1 ? remaining : remaining.substring(0, endOfLine);
            result.push(
                <span key={keyIndex++} className={SQL_COLORS.comment}>{comment}</span>
            );
            remaining = endOfLine === -1 ? '' : remaining.substring(endOfLine);
            matched = true;
            continue;
        }

        // Check for string literals (single quotes)
        if (remaining.startsWith("'")) {
            const endQuote = remaining.indexOf("'", 1);
            if (endQuote !== -1) {
                const str = remaining.substring(0, endQuote + 1);
                result.push(
                    <span key={keyIndex++} className={SQL_COLORS.string}>{str}</span>
                );
                remaining = remaining.substring(endQuote + 1);
                matched = true;
                continue;
            }
        }

        // Check for numbers
        const numberMatch = remaining.match(/^(\d+\.?\d*)/);
        if (numberMatch && (result.length === 0 || /[\s,()=<>+-/*]$/.test(String(result[result.length - 1])))) {
            result.push(
                <span key={keyIndex++} className={SQL_COLORS.number}>{numberMatch[1]}</span>
            );
            remaining = remaining.substring(numberMatch[1].length);
            matched = true;
            continue;
        }

        // Check for special keywords first (higher priority)
        for (const keyword of SPECIAL_KEYWORDS) {
            const regex = new RegExp(`^(${keyword})\\b`, 'i');
            const match = remaining.match(regex);
            if (match) {
                result.push(
                    <span key={keyIndex++} className={SQL_COLORS.special}>{match[1]}</span>
                );
                remaining = remaining.substring(match[1].length);
                matched = true;
                break;
            }
        }
        if (matched) continue;

        // Check for functions
        for (const func of SQL_FUNCTIONS) {
            const regex = new RegExp(`^(${func})(?=\\s*\\()`, 'i');
            const match = remaining.match(regex);
            if (match) {
                result.push(
                    <span key={keyIndex++} className={SQL_COLORS.function}>{match[1]}</span>
                );
                remaining = remaining.substring(match[1].length);
                matched = true;
                break;
            }
        }
        if (matched) continue;

        // Check for SQL keywords
        for (const keyword of SQL_KEYWORDS) {
            const regex = new RegExp(`^(${keyword})\\b`, 'i');
            const match = remaining.match(regex);
            if (match) {
                result.push(
                    <span key={keyIndex++} className={SQL_COLORS.keyword}>{match[1]}</span>
                );
                remaining = remaining.substring(match[1].length);
                matched = true;
                break;
            }
        }
        if (matched) continue;

        // No match - add single character as default
        result.push(
            <span key={keyIndex++} className={SQL_COLORS.default}>{remaining[0]}</span>
        );
        remaining = remaining.substring(1);
    }

    return result;
}

export const QueryTooltip: React.FC<QueryTooltipProps> = ({
    children,
    query,
    latency = "0.45ms",
    scanned,
    type = 'SQL',
    badge,
    onBadgeClick
}) => {
    const [copied, setCopied] = useState(false);

    // Memoize syntax highlighted query
    const highlightedQuery = useMemo(() => {
        if (type === 'SQL') {
            return highlightSQL(query);
        }
        // For JSON/KV, just return as-is with green color
        return <span className="text-green-400">{query}</span>;
    }, [query, type]);

    // Copy SQL to clipboard
    const handleCopy = async (e: React.MouseEvent) => {
        e.stopPropagation();
        try {
            await navigator.clipboard.writeText(query);
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
        } catch (err) {
            console.error('Failed to copy:', err);
        }
    };

    return (
        <TooltipProvider delayDuration={100}>
            <Tooltip>
                <TooltipTrigger asChild>
                    <div className="cursor-help decoration-dotted decoration-slate-400 underline-offset-4">
                        {children}
                    </div>
                </TooltipTrigger>
                <TooltipContent side="bottom" className="max-w-md p-0 border-slate-800 bg-slate-950 text-slate-50 shadow-2xl">
                    <div className="flex items-center justify-between px-3 py-2 border-b border-slate-800 bg-slate-900/50">
                        <div className="flex items-center gap-2">
                            <Badge variant="outline" className="text-[10px] border-slate-700 text-slate-400 font-mono">
                                {type}
                            </Badge>
                            <span className="text-xs font-medium text-slate-300">Query Inspector</span>
                        </div>
                        <div className="flex items-center gap-3 text-[10px] font-mono text-slate-400">
                            <span>⏱ {latency}</span>
                            {scanned && <span>🔍 {scanned}</span>}
                            <button
                                onClick={handleCopy}
                                className="p-1 hover:bg-slate-800 rounded transition-colors"
                                title={copied ? "Copied!" : "Copy SQL"}
                            >
                                {copied ? (
                                    <Check className="h-3 w-3 text-green-400" />
                                ) : (
                                    <Copy className="h-3 w-3 text-slate-400 hover:text-slate-200" />
                                )}
                            </button>
                        </div>
                    </div>
                    {badge && (
                        <div className="px-3 py-1.5 border-b border-slate-800 bg-slate-900/30">
                            {onBadgeClick ? (
                                <Badge
                                    className="text-[10px] bg-amber-500/20 text-amber-400 border-amber-500/30 font-normal cursor-pointer hover:bg-amber-500/30 transition-colors"
                                    onClick={(e) => {
                                        e.stopPropagation();
                                        onBadgeClick();
                                    }}
                                >
                                    {badge} →
                                </Badge>
                            ) : (
                                <Badge className="text-[10px] bg-amber-500/20 text-amber-400 border-amber-500/30 font-normal">
                                    {badge}
                                </Badge>
                            )}
                        </div>
                    )}
                    <div className="p-3 bg-slate-950 overflow-x-auto max-h-64 overflow-y-auto">
                        <pre className="text-xs font-mono whitespace-pre-wrap break-words leading-6 tracking-wide">
                            {highlightedQuery}
                        </pre>
                    </div>
                </TooltipContent>
            </Tooltip>
        </TooltipProvider>
    );
};
