// The entry of the agent's own file (dist/agent.umd.js, chat-widget-agent.js on the site): it puts the runner where the widget's loader
// (lazy-agent.ts) looks for it. The widget itself does not import the agent, so a visitor who only chats never downloads it.
import { AgentRunner } from "./agent";

(window as any).GenieAgentCode = { AgentRunner };
