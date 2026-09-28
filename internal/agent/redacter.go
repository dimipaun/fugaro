package agent

// Redacter returns Redact for secrets with the forms built once, for a
// caller that redacts many strings with the same secrets (fugaro logs, per
// entry and field).
func Redacter(secrets []string) func(string) string {
	forms := redactForms(secrets)
	return func(s string) string { return replaceAll(s, forms) }
}
