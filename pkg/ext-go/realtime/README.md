# Realtime plugin bridge

`pkg/ext-go/realtime` binds `res/int/realtime` to the language-neutral
`intelligence.realtime/v1` Ext plugin contract. A guest provider opens one
explicit model session per process. The host receives a session ID and can
send text or audio, commit a turn, request a response, interrupt, send a tool
result, receive events, and close. Optional audio controls clear uncommitted
input or truncate conversation audio to what was actually played.

Operations are `open`, `send_text`, `send_audio`, `commit_audio`, `respond`,
`interrupt`, `send_tool_result`, `receive`, `clear_input_audio`,
`truncate_audio`, and `close`. Inputs and events are bounded. The bridge
never opens a microphone, plays audio, executes tool calls, or chooses an
endpoint or model. Hosts must authorize calls and close the plugin process.

`testdata/v1` contains language-neutral JSON fixtures for non-Go bindings.
