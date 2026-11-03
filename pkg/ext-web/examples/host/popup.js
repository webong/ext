const el = id => document.getElementById(id);
chrome.storage.local.get(['guestId', 'lastEvent', 'guestState']).then(data => { el('guest').value = data.guestId ?? ''; el('result').textContent = JSON.stringify(data); });
el('save').onclick = async () => { await chrome.storage.local.set({ guestId: el('guest').value.trim() }); el('result').textContent = 'Saved. Reconnect the guest to apply admission.'; };
el('refresh').onclick = async () => { el('peers').textContent = JSON.stringify(await chrome.runtime.sendMessage({ type: 'peers' }), null, 2); };
el('echo').onclick = async () => { const peers = await chrome.runtime.sendMessage({ type: 'peers' }); el('result').textContent = JSON.stringify(await chrome.runtime.sendMessage({ type: 'echo', id: peers[0]?.id, value: el('message').value })); };
