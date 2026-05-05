import React, { useState } from 'react';
import { PostgresPopout } from '@/components/demo/PostgresPopout';
import { ClickstreamPopout } from '@/components/demo/ClickstreamPopout';
import { VeloDBPopout } from '@/components/demo/VeloDBPopout';

export default function PopoutDemo() {
    // Partner ID - could be made dynamic with a selector
    const [partnerId] = useState(44);

    return (
        <div className="min-h-screen bg-slate-100 p-8 space-y-12 font-sans text-slate-900">
            <div className="max-w-7xl mx-auto space-y-12">
                <div className="text-center space-y-2 mb-8">
                    <h1 className="text-4xl font-bold text-slate-900">VeloDB Demo - Live Integration</h1>
                    <p className="text-slate-600">All components now connected to real backend APIs with real-time polling</p>
                </div>

                <div className="space-y-4">
                    <h2 className="text-2xl font-bold text-slate-800 border-b pb-2">1. Postgres Popout (Source)</h2>
                    <PostgresPopout partnerId={partnerId} />
                </div>

                <div className="space-y-4">
                    <h2 className="text-2xl font-bold text-slate-800 border-b pb-2">2. Clickstream Popout (Stream)</h2>
                    <ClickstreamPopout partnerId={partnerId} />
                </div>

                <div className="space-y-4">
                    <h2 className="text-2xl font-bold text-slate-800 border-b pb-2">3. VeloDB Popout (Analytics)</h2>
                    <VeloDBPopout
                        partnerId={partnerId}
                        onRefreshMVs={() => alert('Materialized Views refreshed!')}
                    />
                </div>
            </div>

        </div>
    );
}
