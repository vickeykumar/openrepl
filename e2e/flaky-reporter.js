// A short report of the tests that needed a retry (passed on a later attempt) or failed, written to the job summary
// of the CI run, so that flaky tests show up as a trend and not only when one finally fails twice in a row.
const fs = require("fs");

class FlakyReporter {
  onBegin(config, suite) {
    this.suite = suite;
  }

  onEnd() {
    const flaky = [];
    const failed = [];
    for (const t of this.suite.allTests()) {
      const outcome = t.outcome();
      const name = t.titlePath().filter(Boolean).slice(1).join(" › ");
      if (outcome === "flaky") flaky.push({ name, retries: t.results.length - 1 });
      else if (outcome === "unexpected") failed.push({ name });
    }
    const lines = ["### Browser tests: retries and failures", ""];
    if (!flaky.length && !failed.length) lines.push("No test needed a retry.");
    if (failed.length) {
      lines.push("**Failed (also after the retry):**", "");
      for (const f of failed) lines.push("- " + f.name);
      lines.push("");
    }
    if (flaky.length) {
      lines.push("**Flaky (failed, then passed on a retry):**", "");
      for (const f of flaky) lines.push("- " + f.name);
      lines.push("");
    }
    const text = lines.join("\n") + "\n";
    console.log("\n" + text);
    if (process.env.GITHUB_STEP_SUMMARY) {
      try {
        fs.appendFileSync(process.env.GITHUB_STEP_SUMMARY, text);
      } catch (e) {
        /* the summary is a convenience */
      }
    }
  }
}

module.exports = FlakyReporter;
