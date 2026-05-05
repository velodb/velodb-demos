import { Handle, Position, NodeProps } from "@xyflow/react";
import { MessageCircle } from "lucide-react";

export interface ChatBotNodeData {
  lastMessage?: string;
  onClick?: () => void;
}

const ChatBotNode = ({ data }: NodeProps<ChatBotNodeData>) => {
  const lastMessage = data.lastMessage || "Ask me anything...";

  return (
    <div
      className="rounded-lg border-2 border-green-400 shadow-lg bg-gradient-to-b from-white to-green-50 relative cursor-pointer hover:shadow-xl transition-shadow"
      style={{ width: 150 }}
      onClick={data.onClick}
      data-testid="chatbot-node"
    >
      <Handle type="target" position={Position.Left} className="!bg-green-500" />

      {/* Header */}
      <div className="px-2 py-1.5 border-b border-green-200 flex items-center gap-1.5 bg-green-50/50">
        <MessageCircle className="w-3.5 h-3.5 text-green-600" />
        <span className="font-semibold text-xs text-green-800">Chat</span>
      </div>

      {/* Content - Message Preview */}
      <div className="p-2">
        <div className="text-[9px] text-gray-500 mb-1 font-medium">Last message:</div>
        <div className="text-[10px] text-gray-700 line-clamp-2 leading-relaxed" title={lastMessage}>
          {lastMessage}
        </div>
      </div>
    </div>
  );
};

export default ChatBotNode;
