// Terminal client (T19: webpack 5, TypeScript 4.9, xterm.js 6) and the page
// script (T20).
// Works with Node 12 and later, the version in the build image.
const path = require("path");
const TerserPlugin = require("terser-webpack-plugin");

module.exports = {
    mode: "production",
    entry: {
        // the terminal client, exposed as window.gotty
        "gotty-bundle": {
            import: "./src/main.ts",
            library: { name: "gotty", type: "umd" },
        },
        "preprocessing": "./src/preprocessing.js",
        // the page script for index.html, from src/page/*.js (T20)
        "scribbler": "./src/page/index.js",
    },
    output: {
        path: path.resolve(__dirname, "dist"),
        filename: "[name].js",
        // hterm.js, loaded on demand, sits next to gotty-bundle.js
        chunkFilename: "[name].js",
        publicPath: "auto",
        globalObject: "this",
    },
    devtool: "source-map",
    resolve: {
        extensions: [".ts", ".tsx", ".js"],
    },
    module: {
        rules: [
            {
                test: /\.tsx?$/,
                loader: "ts-loader",
                exclude: /node_modules/
            }
        ]
    },
    optimization: {
        splitChunks: false, // hterm.js holds all of hterm, nothing else is split off
        minimizer: [
            // keep third-party license comments in the bundle itself
            new TerserPlugin({
                extractComments: false,
                terserOptions: { format: { comments: /@license|@preserve|^!/i } },
            }),
        ],
    },
    performance: { hints: false },
};
