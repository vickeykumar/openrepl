// The lists of the quick App Store dialog (src/page/lib-store-filter.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { matches, categories, chipsFor, languages, narrow, pushRecent, importsOf, needed, words } from "../src/page/lib-store-filter.mjs";

const pandas = { name: "pandas", summary: "Tables and time series: read, clean, group and join data.", category: "Data science", repls: ["python", "ipython3"], mod: "pandas" };
const gson = { name: "jar-gson", summary: "JSON for Java objects.", category: "Java libraries", repls: ["java"], source: "com.google.code.gson:gson", mod: "com.google.gson" };
const pytest = { name: "pytest", summary: "Write and run tests.", category: "Dev tools", repls: ["python"] };

test("a search finds words in the summary, the category and the languages, not only in the name", () => {
  assert.equal(matches(pandas, "tables"), true);
  assert.equal(matches(gson, "json"), true);
  assert.equal(matches(pytest, "test"), true);
  assert.equal(matches(pandas, "java"), false);
  assert.equal(matches(gson, "json java"), true, "every word, in any place");
  assert.equal(matches(gson, "json python"), false);
  assert.equal(matches(pandas, ""), true);
  assert.deepEqual(words("  Json,Java  "), ["json", "java"]);
});

test("the category chips count the list, the biggest first, and 'Other' last", () => {
  const list = [pandas, { ...pandas, name: "numpy" }, gson, { name: "x" }, { name: "y" }, { name: "z" }];
  assert.deepEqual(categories(list), [{ name: "Data science", count: 2 }, { name: "Java libraries", count: 1 }, { name: "Other", count: 3 }]);
});

test("a category and a language narrow the list together", () => {
  const list = [pandas, gson, pytest, { name: "x" }];
  assert.deepEqual(narrow(list, "Data science", "").map((v) => v.name), ["pandas"]);
  assert.deepEqual(narrow(list, "", "python").map((v) => v.name), ["pandas", "pytest"]);
  assert.deepEqual(narrow(list, "Dev tools", "java").map((v) => v.name), []);
  assert.deepEqual(narrow(list, "Other", "").map((v) => v.name), ["x"]);
  assert.deepEqual(narrow(list, "", "").length, 4);
  assert.deepEqual(languages(list), ["ipython3", "java", "python"]);
});

test("the recent list keeps three, newest first, with no repeat", () => {
  assert.deepEqual(pushRecent(["a", "b", "c"], "d"), ["d", "a", "b"]);
  assert.deepEqual(pushRecent(["a", "b", "c"], "b"), ["b", "a", "c"]);
  assert.deepEqual(pushRecent(null, "a"), ["a"]);
  assert.deepEqual(pushRecent([1, "a"], "z"), ["z", "a"]);
});

test("the imports of a file, by language", () => {
  assert.deepEqual(importsOf("import numpy as np, pandas.io as pio\nfrom sklearn.linear_model import X\n  import os\n# import nope", "python").sort(), ["numpy", "os", "pandas", "sklearn"]);
  assert.deepEqual(importsOf("const a = require('lodash');\nimport x from '@scope/pkg/sub';\nimport y from './local';\nimport { z } from \"axios\"", "node").sort(), ["@scope/pkg", "axios", "lodash"]);
  assert.deepEqual(importsOf('package main\nimport (\n\t"fmt"\n\t"github.com/spf13/cobra"\n\tyaml "gopkg.in/yaml.v3"\n)\nimport "go.uber.org/zap"', "go").sort(), ["github.com/spf13/cobra", "go.uber.org/zap", "gopkg.in/yaml.v3"]);
  assert.deepEqual(importsOf("import com.google.gson.Gson;\nimport static org.junit.Assert.*;\nimport java.util.List;", "java").sort(), ["com.google.gson.Gson", "java.util.List", "org.junit.Assert"]);
  assert.deepEqual(importsOf("use serde_json::Value;\nextern crate rand;\n:dep regex = \"1\"", "evcxr").sort(), ["rand", "regex", "serde_json"]);
  assert.deepEqual(importsOf("import numpy", "bash"), []);
  assert.deepEqual(importsOf("", "python"), []);
});

test("the apps an open file needs are the ones not on, found by what it imports", () => {
  const views = [{ ...pandas, state: "available" }, { ...gson, state: "installed", on: false }, { name: "numpy", mod: "numpy", state: "installed", on: true }, { name: "cobra", mod: "github.com/spf13/cobra", state: "available" }, { name: "bad", mod: "bad", state: "failed" }];
  assert.deepEqual(needed(["pandas", "numpy", "bad", "os"], views).map((v) => v.name), ["pandas"], "an app that is on or failed is not offered");
  assert.deepEqual(needed(["com.google.gson.Gson"], views).map((v) => v.name), ["jar-gson"], "a Java import lies inside the package");
  assert.deepEqual(needed(["github.com/spf13/cobra/doc"], views).map((v) => v.name), ["cobra"], "a Go path lies inside the module");
  assert.deepEqual(needed([], views), []);
});

test("the chips keep every category of the list, with the counts of what a search found, so an emptied category stays on the row", () => {
  const whole = [pandas, { ...pandas, name: "numpy" }, gson, pytest];
  assert.deepEqual(chipsFor(whole, [gson]), [{ name: "Data science", count: 0 }, { name: "Dev tools", count: 0 }, { name: "Java libraries", count: 1 }].sort((a, b) => 0));
  assert.deepEqual(chipsFor(whole, whole).map((c) => c.count), [2, 1, 1]);
  assert.deepEqual(chipsFor(whole, []).map((c) => c.count), [0, 0, 0]);
});
