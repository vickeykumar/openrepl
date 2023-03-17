const UglifyJSPlugin = require('uglifyjs-webpack-plugin');

module.exports = {
    entry: {
        "gotty-bundle": "./src/main.ts",
        "preprocessing": "./src/preprocessing.js"
    },
    output: {
        filename: "./dist/[name].js",
        library: 'gotty',
        libraryTarget: 'umd',

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
            },
            {
                test: /\.js$/,
                include: /node_modules/,
                loader: 'license-loader'
            }
        ]
    },
    plugins: [
        new UglifyJSPlugin()
    ]
};
