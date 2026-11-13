// The page script for index.html (T20), in load order. Each part runs in
// this order, as the sections of the old single scribbler.js did.
import "./00-utils";
import "./01-session";
import "./02-layout";
import "./03-run-and-files";
import "./04-repl-demo";
import "./05-auth";
import "./06-editor";
import "./22-editor-tabs"; // before the Files panel, which tells it about the tree
import "./23-tab-lanes";
import "./24-large-file-viewer";
import "./25-file-search";
import "./26-file-marks";
import "./27-preview";
import "./28-unsaved";
import "./07-file-browser";
import "./08-genie-nudge";
import "./09-hero";
import "./10-workspace-controls";
import "./11-terminal-state";
import "./12-landing";
import "./13-mobile";
import "./14-accessibility";
import "./15-files-empty";
import "./16-language-pages";
import "./17-share-code";
import "./18-genie-review";
import "./19-genie-actions";
import "./20-tips";
import "./21-ide-theme";
import "./29-web-language"; // HTML and CSS in the language picker
