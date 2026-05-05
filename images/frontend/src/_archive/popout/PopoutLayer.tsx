import React, { useState } from 'react';
import { usePopout } from '@/context/PopoutContext';
import { PostgresPopout } from '@/components/demo/PostgresPopout';
import { ClickstreamPopout } from '@/components/demo/ClickstreamPopout';
import { VeloDBPopout } from '@/components/demo/VeloDBPopout';
import { Button } from '@/components/ui/button';
import { X } from 'lucide-react';

export const PopoutLayer: React.FC = () => {
    const { activePopout, closePopout } = usePopout();

    // Partner ID for all popouts
    const [partnerId] = useState(44);

    if (!activePopout) {
        return null;
    }

    return (
        <>
            {/* Backdrop */}
            <div
                className="fixed inset-0 bg-black/50 backdrop-blur-sm z-[100]"
                onClick={closePopout}
            />

            {/* Popout Container */}
            <div className="fixed inset-0 z-[101] flex items-center justify-center pointer-events-none">
                <div className="pointer-events-auto relative max-h-[90vh] overflow-auto p-4 animate-in zoom-in-95 duration-200">
                    <Button
                        variant="ghost"
                        size="icon"
                        className="absolute top-2 right-2 z-10 bg-white/80 hover:bg-white rounded-full shadow-sm"
                        onClick={closePopout}
                    >
                        <X className="h-4 w-4" />
                    </Button>

                    {activePopout === 'postgres' && (
                        <PostgresPopout partnerId={partnerId} />
                    )}

                    {activePopout === 'clickstream' && (
                        <ClickstreamPopout partnerId={partnerId} />
                    )}

                    {activePopout === 'velodb' && (
                        <VeloDBPopout
                            partnerId={partnerId}
                            onRefreshMVs={() => alert('Refreshed Materialized Views!')}
                        />
                    )}
                </div>
            </div>

        </>
    );
};
