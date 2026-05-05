import React, { createContext, useContext, useState, ReactNode } from 'react';

type PopoutType = 'postgres' | 'clickstream' | 'velodb' | null;

interface PopoutContextType {
    activePopout: PopoutType;
    openPopout: (type: PopoutType) => void;
    closePopout: () => void;
    isPopoutOpen: boolean;
}

const PopoutContext = createContext<PopoutContextType | undefined>(undefined);

export const PopoutProvider: React.FC<{ children: ReactNode }> = ({ children }) => {
    const [activePopout, setActivePopout] = useState<PopoutType>(null);

    const openPopout = (type: PopoutType) => setActivePopout(type);
    const closePopout = () => setActivePopout(null);

    return (
        <PopoutContext.Provider value={{ activePopout, openPopout, closePopout, isPopoutOpen: !!activePopout }}>
            {children}
        </PopoutContext.Provider>
    );
};

export const usePopout = () => {
    const context = useContext(PopoutContext);
    if (context === undefined) {
        throw new Error('usePopout must be used within a PopoutProvider');
    }
    return context;
};
