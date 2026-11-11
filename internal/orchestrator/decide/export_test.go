package decide

// Prompt is the system prompt the runner appends to every decision call
// (--append-system-prompt). The tests hold it against the fence the brief
// renders: the prompt is the one place that says the block is data, so a
// delimiter renamed in one of the two and not the other is a fence the model
// was never told about.
var Prompt = systemPrompt

// Cut is what a brief says where its body was cut, so a test can hold a brief
// over its cap to the promise that the loss is not silent.
var Cut = briefCut
