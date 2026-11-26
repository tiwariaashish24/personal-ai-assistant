const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const vm = require("node:vm");
const path = require("node:path");

test("AudioWorklet forwards copied microphone frames and keeps output silent", async () => {
  const source = await fs.readFile(path.join(__dirname, "voice-capture-worklet.js"), "utf8");
  const messages = [];
  let processorName;
  let Processor;
  const context = {
    AudioWorkletProcessor: class {
      constructor() {
        this.port = {
          postMessage(data, transfer) {
            messages.push({ data, transfer });
          },
        };
      }
    },
    Float32Array,
    registerProcessor(name, processor) {
      processorName = name;
      Processor = processor;
    },
  };
  vm.runInNewContext(source, context);

  const processor = new Processor();
  const input = new Float32Array([0.25, -0.5, 0.75]);
  const output = [[new Float32Array(3).fill(1)]];
  assert.equal(processor.process([[input]], output), true);
  assert.equal(processorName, "jarvis-capture");
  assert.deepEqual(Array.from(messages[0].data), Array.from(input));
  assert.notEqual(messages[0].data, input);
  assert.deepEqual(Array.from(messages[0].transfer), [messages[0].data.buffer]);
  assert.deepEqual(Array.from(output[0][0]), [0, 0, 0]);

  processor.process([[]], [[]]);
  assert.equal(messages.length, 1);
});
