import React from 'react';
import { X, Zap, Clock, Database, Users, RefreshCw, ArrowRight, Target } from 'lucide-react';

// SQL syntax highlighting
const SQL_COLORS = {
  keyword: 'text-blue-600 font-semibold',
  special: 'text-purple-600 font-semibold',
  function: 'text-cyan-600',
  string: 'text-green-600',
  number: 'text-amber-600',
  comment: 'text-gray-400 italic',
  default: 'text-gray-700',
};

interface MaterializedViewCardProps {
  isOpen: boolean;
  onClose: () => void;
}

export const MaterializedViewCard: React.FC<MaterializedViewCardProps> = ({ isOpen, onClose }) => {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div
        className="bg-white rounded-xl shadow-2xl max-w-4xl w-full max-h-[90vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200 bg-gradient-to-r from-purple-50 to-white">
          <div className="flex items-center gap-3">
            <div className="p-2 bg-purple-100 rounded-lg">
              <Database className="h-6 w-6 text-purple-600" />
            </div>
            <div>
              <h2 className="text-xl font-bold text-gray-900">Materialized View</h2>
              <p className="text-sm text-gray-500">Pre-Computed Analytics for Instant Dashboards</p>
            </div>
          </div>
          <button
            onClick={onClose}
            className="p-2 hover:bg-gray-100 rounded-lg transition-colors"
          >
            <X className="h-5 w-5 text-gray-500" />
          </button>
        </div>

        {/* What is it section */}
        <div className="px-6 py-4 bg-purple-50 border-b border-purple-100">
          <div className="flex items-start gap-3">
            <div className="p-1.5 bg-purple-200 rounded-full mt-0.5">
              <Zap className="h-4 w-4 text-purple-700" />
            </div>
            <div>
              <h3 className="font-semibold text-purple-900 mb-1">What is a Materialized View?</h3>
              <p className="text-sm text-purple-800">
                Pre-calculated results that VeloDB keeps ready. Instead of counting millions of events
                every time a customer loads their dashboard, the answer is already computed and waiting.
              </p>
            </div>
          </div>
        </div>

        {/* CREATE MV SQL */}
        <div className="px-6 py-4 border-b border-gray-200">
          <h3 className="text-sm font-semibold text-gray-700 mb-3 flex items-center gap-2">
            <RefreshCw className="h-4 w-4 text-purple-500" />
            The Materialized View Definition
          </h3>
          <div className="bg-gray-900 rounded-lg p-4 overflow-x-auto">
            <pre className="text-sm font-mono leading-relaxed">
              <span className={SQL_COLORS.special}>CREATE MATERIALIZED VIEW</span>
              <span className="text-gray-300"> mv_conversion_funnel</span>
              {'\n'}
              <span className={SQL_COLORS.special}>REFRESH ASYNC EVERY</span>
              <span className="text-amber-400"> 1 </span>
              <span className={SQL_COLORS.special}>MINUTE</span>
              {'\n'}
              <span className={SQL_COLORS.keyword}>AS</span>
              {'\n'}
              <span className={SQL_COLORS.keyword}>SELECT</span>
              {'\n'}
              <span className="text-gray-300">{'  '}partner_id,</span>
              {'\n'}
              <span className="text-gray-300">{'  '}</span>
              <span className={SQL_COLORS.function}>SUM</span>
              <span className="text-gray-300">(</span>
              <span className={SQL_COLORS.keyword}>CASE WHEN</span>
              <span className="text-gray-300"> event_type = </span>
              <span className={SQL_COLORS.string}>'view'</span>
              {'\n'}
              <span className="text-gray-300">{'      '}</span>
              <span className={SQL_COLORS.keyword}>THEN</span>
              <span className="text-amber-400"> 1 </span>
              <span className={SQL_COLORS.keyword}>ELSE</span>
              <span className="text-amber-400"> 0 </span>
              <span className={SQL_COLORS.keyword}>END</span>
              <span className="text-gray-300">) </span>
              <span className={SQL_COLORS.keyword}>as</span>
              <span className="text-gray-300"> views,</span>
              {'\n'}
              <span className="text-gray-300">{'  '}</span>
              <span className={SQL_COLORS.function}>SUM</span>
              <span className="text-gray-300">(</span>
              <span className={SQL_COLORS.keyword}>CASE WHEN</span>
              <span className="text-gray-300"> event_type = </span>
              <span className={SQL_COLORS.string}>'cart'</span>
              <span className="text-gray-300"> ...</span>
              <span className="text-gray-300">) </span>
              <span className={SQL_COLORS.keyword}>as</span>
              <span className="text-gray-300"> carts,</span>
              {'\n'}
              <span className="text-gray-300">{'  '}</span>
              <span className={SQL_COLORS.function}>SUM</span>
              <span className="text-gray-300">(</span>
              <span className={SQL_COLORS.keyword}>CASE WHEN</span>
              <span className="text-gray-300"> event_type = </span>
              <span className={SQL_COLORS.string}>'purchase'</span>
              <span className="text-gray-300"> ...</span>
              <span className="text-gray-300">) </span>
              <span className={SQL_COLORS.keyword}>as</span>
              <span className="text-gray-300"> purchases</span>
              {'\n'}
              <span className={SQL_COLORS.keyword}>FROM</span>
              <span className="text-gray-300"> fact_clickstream</span>
              {'\n'}
              <span className={SQL_COLORS.keyword}>WHERE</span>
              <span className="text-gray-300"> event_time &gt;= </span>
              <span className={SQL_COLORS.function}>DATE_SUB</span>
              <span className="text-gray-300">(</span>
              <span className={SQL_COLORS.function}>NOW</span>
              <span className="text-gray-300">(), </span>
              <span className={SQL_COLORS.keyword}>INTERVAL</span>
              <span className="text-amber-400"> 7 </span>
              <span className={SQL_COLORS.keyword}>DAY</span>
              <span className="text-gray-300">)</span>
              {'\n'}
              <span className={SQL_COLORS.keyword}>GROUP BY</span>
              <span className="text-gray-300"> partner_id;</span>
            </pre>
          </div>

          {/* Key callouts */}
          <div className="flex flex-wrap gap-2 mt-3">
            <span className="inline-flex items-center gap-1.5 px-3 py-1 bg-purple-100 text-purple-700 rounded-full text-xs font-medium">
              <RefreshCw className="h-3 w-3" />
              Auto-refresh every 1 minute
            </span>
            <span className="inline-flex items-center gap-1.5 px-3 py-1 bg-blue-100 text-blue-700 rounded-full text-xs font-medium">
              <Users className="h-3 w-3" />
              Pre-aggregated per customer
            </span>
            <span className="inline-flex items-center gap-1.5 px-3 py-1 bg-amber-100 text-amber-700 rounded-full text-xs font-medium">
              <Clock className="h-3 w-3" />
              7-day rolling window
            </span>
          </div>
        </div>

        {/* Before/After Comparison */}
        <div className="px-6 py-4">
          <h3 className="text-sm font-semibold text-gray-700 mb-4">Before vs After Comparison</h3>

          <div className="grid grid-cols-[1fr,auto,1fr] gap-4 items-stretch">
            {/* Before */}
            <div className="bg-red-50 border border-red-200 rounded-lg p-4">
              <div className="flex items-center gap-2 mb-3">
                <div className="p-1.5 bg-red-200 rounded-full">
                  <Clock className="h-4 w-4 text-red-600" />
                </div>
                <span className="font-semibold text-red-700">Without MV</span>
              </div>

              <div className="bg-gray-900 rounded-lg p-3 mb-3 overflow-x-auto">
                <pre className="text-xs font-mono text-gray-300 leading-relaxed">
                  <span className="text-gray-500">-- Every dashboard load</span>
                  {'\n'}
                  <span className="text-blue-400">SELECT</span>
                  {'\n'}
                  <span>{'  '}SUM(CASE WHEN...)</span>
                  {'\n'}
                  <span className="text-blue-400">FROM</span>
                  <span> fact_clickstream</span>
                  {'\n'}
                  <span className="text-blue-400">WHERE</span>
                  <span> partner_id = 44</span>
                  {'\n'}
                  <span>{'  '}</span>
                  <span className="text-blue-400">AND</span>
                  <span> event_time &gt;= ...</span>
                </pre>
              </div>

              <div className="space-y-2">
                <div className="flex items-center gap-2 text-sm">
                  <Clock className="h-4 w-4 text-red-500" />
                  <span className="text-red-700 font-bold">144ms</span>
                  <span className="text-gray-500">response time</span>
                </div>
                <div className="flex items-center gap-2 text-sm">
                  <Database className="h-4 w-4 text-red-500" />
                  <span className="text-red-700 font-bold">1M+ rows</span>
                  <span className="text-gray-500">scanned</span>
                </div>
                <div className="flex items-center gap-2 text-sm">
                  <Users className="h-4 w-4 text-red-500" />
                  <span className="text-red-700 font-bold">~100</span>
                  <span className="text-gray-500">concurrent users</span>
                </div>
              </div>
            </div>

            {/* Arrow */}
            <div className="flex flex-col items-center justify-center">
              <div className="p-2 bg-purple-100 rounded-full mb-2">
                <ArrowRight className="h-5 w-5 text-purple-600" />
              </div>
              <span className="text-xs font-medium text-purple-600 whitespace-nowrap">VeloDB MV</span>
            </div>

            {/* After */}
            <div className="bg-green-50 border border-green-200 rounded-lg p-4">
              <div className="flex items-center gap-2 mb-3">
                <div className="p-1.5 bg-green-200 rounded-full">
                  <Zap className="h-4 w-4 text-green-600" />
                </div>
                <span className="font-semibold text-green-700">With MV</span>
              </div>

              <div className="bg-gray-900 rounded-lg p-3 mb-3 overflow-x-auto">
                <pre className="text-xs font-mono text-gray-300 leading-relaxed">
                  <span className="text-gray-500">-- Instant lookup</span>
                  {'\n'}
                  <span className="text-blue-400">SELECT</span>
                  {'\n'}
                  <span>{'  '}views, carts, purchases</span>
                  {'\n'}
                  <span className="text-blue-400">FROM</span>
                  <span> mv_conversion_funnel</span>
                  {'\n'}
                  <span className="text-blue-400">WHERE</span>
                  <span> partner_id = 44;</span>
                </pre>
              </div>

              <div className="space-y-2">
                <div className="flex items-center gap-2 text-sm">
                  <Zap className="h-4 w-4 text-green-500" />
                  <span className="text-green-700 font-bold">25ms</span>
                  <span className="text-gray-500">response time</span>
                </div>
                <div className="flex items-center gap-2 text-sm">
                  <Target className="h-4 w-4 text-green-500" />
                  <span className="text-green-700 font-bold">1 row</span>
                  <span className="text-gray-500">lookup</span>
                </div>
                <div className="flex items-center gap-2 text-sm">
                  <Users className="h-4 w-4 text-green-500" />
                  <span className="text-green-700 font-bold">10,000+</span>
                  <span className="text-gray-500">concurrent users</span>
                </div>
              </div>
            </div>
          </div>
        </div>

        {/* Bottom Stats */}
        <div className="px-6 py-4 bg-gray-50 border-t border-gray-200">
          <div className="grid grid-cols-3 gap-4 mb-4">
            <div className="text-center">
              <div className="text-2xl font-bold text-purple-600">6x</div>
              <div className="text-sm text-gray-600">Faster Response</div>
            </div>
            <div className="text-center">
              <div className="text-2xl font-bold text-purple-600">99.99%</div>
              <div className="text-sm text-gray-600">Less Data Scanned</div>
            </div>
            <div className="text-center">
              <div className="text-2xl font-bold text-purple-600">100x</div>
              <div className="text-sm text-gray-600">More Concurrent Users</div>
            </div>
          </div>

          <div className="text-center py-3 bg-gradient-to-r from-purple-100 to-amber-100 rounded-lg">
            <p className="text-gray-700 font-medium">
              "Pre-compute once per minute, serve <span className="text-purple-700 font-bold">thousands</span> instantly"
            </p>
          </div>
        </div>
      </div>
    </div>
  );
};

export default MaterializedViewCard;
