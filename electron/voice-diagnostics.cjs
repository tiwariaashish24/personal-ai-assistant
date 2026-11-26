const fs = require("node:fs/promises");
const path = require("node:path");

const maxDiagnosticLogBytes = 1024 * 1024;

async function appendVoiceDiagnostic(filePath, record) {
  if (!record || typeof record.stage !== "string" ||
      !/^[a-z][a-z0-9-]{0,39}$/u.test(record.stage) ||
      typeof record.message !== "string") {
    throw new Error("Invalid voice diagnostic record.");
  }
  const message = record.message
    .replace(/[\u0000-\u001f\u007f-\u009f]/gu, " ")
    .slice(0, 1000);
  const line = `${JSON.stringify({
    timestamp: new Date().toISOString(),
    stage: record.stage,
    message,
  })}\n`;
  await fs.mkdir(path.dirname(filePath), { recursive: true });
  let currentSize = 0;
  try {
    currentSize = (await fs.stat(filePath)).size;
  } catch (error) {
    if (error.code !== "ENOENT") {
      throw new Error(`Could not inspect voice diagnostics log: ${error.message}`);
    }
  }
  if (currentSize + Buffer.byteLength(line, "utf8") > maxDiagnosticLogBytes) {
    await fs.writeFile(filePath, line, { encoding: "utf8", mode: 0o600 });
  } else {
    await fs.appendFile(filePath, line, { encoding: "utf8", mode: 0o600 });
  }
}

module.exports = { appendVoiceDiagnostic };
