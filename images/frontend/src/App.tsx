import { Toaster } from "@/components/ui/toaster";
import { Toaster as Sonner } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Routes, Route } from "react-router-dom";
import CustomerPage from "./pages/CustomerPage";
import Observability from "./pages/Observability";
import GenAI from "./pages/GenAI";
import Lakehouse from "./pages/Lakehouse";
import Backup from "./pages/Backup";
import SettingsPage from "./pages/SettingsPage";
import Query from "./pages/Query";
import RAGDemo from "./pages/RAGDemo";
import NotFound from "./pages/NotFound";

const queryClient = new QueryClient();

const App = () => (
  <QueryClientProvider client={queryClient}>
    <TooltipProvider>
      <Toaster />
      <Sonner />
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<CustomerPage />} />
          <Route path="/observability" element={<Observability />} />
          <Route path="/genai" element={<GenAI />} />
          <Route path="/rag" element={<RAGDemo />} />
          <Route path="/lakehouse" element={<Lakehouse />} />
          <Route path="/backup" element={<Backup />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="/query" element={<Query />} />
          {/* ADD ALL CUSTOM ROUTES ABOVE THE CATCH-ALL "*" ROUTE */}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </BrowserRouter>
    </TooltipProvider>
  </QueryClientProvider>
);

export default App;
