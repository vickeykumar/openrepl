---
title: Run TypeScript files with Node.js
keywords: TypeScript, ts-node, tsc, compiler, NodeNext, ERR_MODULE_NOT_FOUND, greeter.js, moduleResolution, run button
link: /doc.html#installing-apps
---
The TypeScript REPL (`ts-node`) is for entering snippets. To run a saved TypeScript program with imports such as `import { greet } from "./greeter.js"`, compile it with `tsc` using NodeNext module rules, then run the emitted JavaScript with Node. From the project directory, a standalone example is `mkdir -p dist && tsc --target ES2022 --module NodeNext --moduleResolution NodeNext --types node --typeRoots "$(env -u NPM_CONFIG_PREFIX npm root -g)/@types" --rootDir . --outDir dist main.ts && node dist/main.js`. NodeNext lets TypeScript resolve a `.js` import to the corresponding `.ts` source while emitting JavaScript files Node can load. If the project has a `tsconfig.json`, follow that project's compiler settings instead of overriding them. In OpenREPL, use the Run button for a saved file; the TypeScript Run setup compiles standalone files and executes the emitted JavaScript.
