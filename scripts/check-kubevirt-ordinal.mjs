#!/usr/bin/env node
// Pre-flight gate for the kubevirt substrate ordinal. This is a surface that EXECUTES: it runs on the source
// tree, where the text actually lives, which no bed phase can do (opencharly/charly#928 — a bed image resolves
// a locally-named candy from a published ref, and a context:[build] plan step has no phase).
//
// It FAILS when the ordinal regresses, which is the property the R10 class gate requires and which the bed's
// step could not provide. Verified failing on the old text before being trusted.
import { readFileSync } from 'node:fs';

const manifest = 'candy/plugin-kubevirt/charly.yml';
const src = readFileSync(manifest, 'utf8');
// the text WRAPS: line 4 ends "... the 7th deploy" and line 5 begins "SUBSTRATE (". Match the real line.
const good = /the 7th deploy\b/;
const bad = /the 6th deploy\b/;

if (bad.test(src)) {
  console.error(`${manifest}: still says the 6th deploy; #SubstrateKind's enum makes kubevirt the 7th`);
  process.exit(1);
}
if (!good.test(src)) {
  console.error(`${manifest}: does not state "the 7th deploy"`);
  process.exit(1);
}
console.log(`${manifest}: states the 7th deploy, matching #SubstrateKind's enum order`);
