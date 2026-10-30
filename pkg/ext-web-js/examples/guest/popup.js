const el = id => document.getElementById(id);
chrome.storage.local.get('hostId').then(data => { el('host').value = data.hostId ?? ''; });
el('save').onclick = async () => { el('result').textContent = JSON.stringify(await chrome.runtime.sendMessage({ type: 'configure', hostId: el('host').value.trim() })); };
el('status').onclick = async () => { el('result').textContent = JSON.stringify(await chrome.runtime.sendMessage({ type: 'status' })); };
el('greet').onclick = async () => { el('result').textContent = JSON.stringify(await chrome.runtime.sendMessage({ type: 'call', method: 'host.greet', value: 'guest' })); };
el('denied').onclick = async () => { el('result').textContent = JSON.stringify(await chrome.runtime.sendMessage({ type: 'call', method: 'host.secret', value: 'guest' })); };
