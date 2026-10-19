# Fugaro Voice Module Spec

Oct 18, 2026 · Dimi Paun

## Goal

Hands-free voice interface for the Fugaro orchestrator, starting as a separable module in the Electron console, later in the phone app. App stays in the foreground while talking (no background listening).

## Architecture

Pipeline, not speech-to-speech: speech-to-text → orchestrator/LLM → text-to-speech. Chosen for control over filtering and summarising task events.

One common interface per direction (audio frames in → partial/final transcripts out; text in → audio stream out). Backends are swappable; the rest of the app never knows which is active.

## Speech-to-text

- macOS / iOS: Apple SpeechAnalyzer (on-device, OS 26), via a small native Swift helper process from Electron
- Android: Google on-device SpeechRecognizer
- Linux / Windows: whisper.cpp (also shipped on Mac as a baseline for comparison)
- Cloud: Deepgram and AssemblyAI behind the cloud toggle; test both (AssemblyAI cheaper, roughly $0.15–0.45/hr streaming vs Deepgram ~$0.46/hr; Deepgram offers $200 free credits vs $50). Decide on accuracy and latency.
- Front end: Silero VAD so only speech is transcribed; wake word "Fugaro" via openWakeWord or Porcupine routes speech to the app vs the active agent.

## Text-to-speech

- Local: Kokoro on all platforms (one voice, one code path); Apple voices as fallback
- Cloud: Cartesia (low-latency streaming); ElevenLabs possible later
- Key metric: time to first audio; stream speech while text is still generating.

## Engine toggle and experiments

- Cloud icon on the voice screen switches local/cloud (three-way on Mac: Apple, whisper.cpp, cloud).
- Record raw session audio to replay the same clips through each engine.
- Log latency to first partial, latency to final, and CPU/battery load.

## Keys and security

- Provider API keys stored at project level in the per-project security service; never baked into apps.
- No keys → cloud option greyed out; fall back to local.
- Security service mints short-lived temporary tokens per session (Deepgram, AssemblyAI, Cartesia support this); clients stream directly to providers. No long-lived keys on devices.

## Filtering and summarising

1. Rules-based classifier per event: urgent (job failed, spending cap hit, agent asking a question), mention (PR ready, task done), silent (edits, tool calls, retries → dashboard only).
2. Debounce window batches bursts into one utterance.
3. Small fast LLM (e.g. Haiku) turns each batch into one concise spoken line.
4. On-demand digest: "what's going on?"
5. Interrupt switch: urgent items cut in immediately or wait for a pause.

## Barge-in

- Echo cancellation: Apple voice-processing mode, Android voice-communication AEC, Chromium WebRTC echoCancellation in Electron. TTS must play through the same audio pipeline the canceller monitors (not a separate native player).
- Detection: duck volume instantly on VAD; stop for real after ~300 ms of speech or first transcribed word (ignores coughs, "mm-hmm").
- Cleanup: cancel playback, pending TTS stream and LLM generation; record how much of the reply was actually heard and pass that to the agent.

## Phone-specific considerations

- Audio routing: speaker, AirPods, car Bluetooth; Bluetooth headset mics drop to low call quality and hurt transcription; headphones simplify echo.
- Interruptions: calls, Siri, other apps taking the audio session; pause and resume gracefully.
- Keep the screen awake on the voice screen.
- Network handoffs (Wi-Fi ↔ cellular): auto-reconnect cloud streams or fall back to local mid-conversation.
- Model size: Kokoro/Whisper models are tens to hundreds of MB; download on demand rather than bundling.
