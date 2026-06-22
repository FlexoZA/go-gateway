// Generates golden universal-webhook outputs by running the ORIGINAL JS adapter
// from dfm-mvr-gateway against the committed fixtures. This locks the Go port to
// byte-for-byte parity with production.
//
// Usage: node tools/gen-webhook-golden.mjs [path-to-dfm-mvr-gateway]
import { readFileSync, writeFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

const dfmRoot = process.argv[2] || '/home/christiaan/projects/dfm-mvr-gateway';
const adapterPath = resolve(dfmRoot, 'src/core/adapters/universalWebhookAdapter.js');
const { buildUniversalWebhookMessage } = await import(pathToFileURL(adapterPath).href);

const fixturesPath = new URL('../internal/core/webhook/testdata/fixtures.json', import.meta.url);
const goldenPath = new URL('../internal/core/webhook/testdata/golden.json', import.meta.url);

const fixtures = JSON.parse(readFileSync(fixturesPath, 'utf8'));
// Process in array order so the per-device seq_no counter matches the Go test,
// which reuses a single client across the fixtures in order.
const out = fixtures.map((f) => ({
  name: f.name,
  output: buildUniversalWebhookMessage(f.input, f.options)
}));

writeFileSync(goldenPath, JSON.stringify(out, null, 2) + '\n');
console.log(`wrote ${out.length} golden outputs to ${goldenPath.pathname}`);
