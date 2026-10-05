const fs = require('node:fs');
const assert = require('node:assert/strict');
const [addon, guest, descriptor, ...files] = process.argv.slice(2);
const binding = require(addon);
const selected = fs.readFileSync(descriptor, 'utf8');
const requests = files.map(f => fs.readFileSync(f, 'utf8'));
// All policies in this demonstration admit explicitly selected local fixtures.
let ticks = 0;
const timer = setInterval(() => ticks++, 1);
binding.run(guest, selected, requests).then(responses => {
  for (const response of responses) console.log(response);
  // Also ensure a waiting guest doesn't block the event loop.
  const wait = JSON.parse(requests[0]);
  wait.operation = 'wait'; wait.deadline = new Date(Date.now() + 100).toISOString();
  return assert.rejects(binding.run(guest, selected, [JSON.stringify(wait)]));
}).then(() => {
  clearInterval(timer);
  assert.ok(ticks > 0, 'native work blocked the event loop');
}).catch(err => { clearInterval(timer); console.error(err); process.exitCode = 1; });
