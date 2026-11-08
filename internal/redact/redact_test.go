package redact

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fake joins the pieces of a fixture. Nothing here is written whole: a test
// file in this repository must not carry a secret-shaped literal, and a
// pattern pass is tested on shapes, not on values.
func fake(parts ...string) string { return strings.Join(parts, "") }

// patternCases are what the exact pass cannot know: credentials Fugaro never
// held, printed by something else — a failing cloud call, a provider's error
// body, a signed URL in a message, a key block in a Terraform stream, the
// password of a connection string or of a command line.
var patternCases = []struct {
	name string
	in   string
	leak string
	keep string
}{
	{
		name: "anthropic key",
		in:   "api error: " + fake("sk-", "ant-api03-", strings.Repeat("Qw3Z", 8)) + " was rejected",
		leak: fake("sk-", "ant-api03-"),
		keep: "was rejected",
	},
	{
		name: "openai key",
		in:   "curl: " + fake("sk-", "proj-", strings.Repeat("Ab3d", 7)),
		leak: fake("sk-", "proj-"),
	},
	{
		name: "stripe key",
		in:   "charge refused: " + fake("sk_", "live_", strings.Repeat("Aa1", 8)),
		leak: fake("sk_", "live_"),
	},
	{
		name: "github token",
		in:   "remote: " + fake("ghp_", strings.Repeat("A1b2C3d4", 5)) + " is not authorised",
		leak: fake("ghp_"),
		keep: "is not authorised",
	},
	{
		name: "github fine-grained token",
		in:   fake("github_pat_", strings.Repeat("A1b2C3d4e5", 3)),
		leak: fake("github_pat_"),
	},
	{
		name: "gitlab token",
		in:   fake("glpat-", strings.Repeat("Xy9", 8)),
		leak: fake("glpat-"),
	},
	{
		name: "slack token",
		in:   fake("xoxb-", "123456789012-", "abcdefghijklmnop"),
		leak: fake("xoxb-"),
	},
	{
		name: "aws access key id",
		in:   "SignatureDoesNotMatch for " + fake("AKIA", strings.Repeat("QWERTYUI", 2)),
		leak: fake("AKIA"),
	},
	{
		name: "google api key",
		in:   "maps refused: " + fake("AIza", strings.Repeat("SyD", 12)),
		leak: fake("AIza"),
	},
	{
		name: "google oauth token",
		in:   fake("ya29.", strings.Repeat("0Ab-", 6)),
		leak: fake("ya29."),
	},
	{
		name: "jwt",
		in:   "answer: " + fake("eyJ", "hbGciOiJIUzI1NiJ9.", "eyJzdWIiOiIxIn0.", "c2lnbmF0dXJlMTIz") + " expired",
		leak: fake("eyJ", "hbGciOiJIUzI1NiJ9."),
		keep: "expired",
	},
	{
		name: "authorization header",
		in:   "Authorization: Bearer " + fake(strings.Repeat("A1b2C3d4", 3)) + "\nnext line",
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: "next line",
	},
	{
		name: "lowercase proxy header",
		in:   "proxy-authorization: basic " + fake(strings.Repeat("dXNlcjpw", 3)) + "\n",
		leak: strings.Repeat("dXNlcjpw", 3),
	},
	{
		name: "api key header",
		in:   "x-api-key: " + fake(strings.Repeat("Zx9", 8)) + "\n",
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "api key header with an underscore",
		in:   "API_KEY: " + fake(strings.Repeat("Zx9", 8)) + "\n",
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "underscore api key inside a json object",
		in: `{"api_key":"` + fake(strings.Repeat("Zx9", 8)) +
			`","message":"quota exceeded for project foo","step":"terraform apply"}`,
		leak: strings.Repeat("Zx9", 8),
		keep: `"step":"terraform apply"}`,
	},
	{
		name: "header named inside a json object",
		in: `{"severity":"ERROR","x-api-key":"` + fake(strings.Repeat("Zx9", 8)) +
			`","message":"quota exceeded for project foo","step":"terraform apply"}`,
		leak: strings.Repeat("Zx9", 8),
		keep: `"message":"quota exceeded for project foo","step":"terraform apply"}`,
	},
	{
		name: "quoted header value in plain text",
		in:   `Authorization: "Bearer ` + fake(strings.Repeat("A1b2C3d4", 3)) + `" and then some`,
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: "and then some",
	},
	{
		name: "quoted header value that is never closed",
		in:   `Authorization: "Bearer ` + fake(strings.Repeat("A1b2C3d4", 3)),
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "header escaped inside a json string",
		in: `{"content":"{\"x-api-key\":\"` + fake(strings.Repeat("Zx9", 8)) +
			`\",\"message\":\"quota exceeded for project foo\",\"step\":\"terraform apply\"}"}`,
		leak: strings.Repeat("Zx9", 8),
		keep: `\"message\":\"quota exceeded for project foo\",\"step\":\"terraform apply\"`,
	},
	{
		name: "escaped header value escaped a level deeper",
		in: `{"content":"{\"x-api-key\":\"a\\\"` + fake(strings.Repeat("Zx9", 8)) +
			`\"}"}`,
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "escaped header value that is never closed",
		in:   `{"content":"{\"api-key\":\"` + fake(strings.Repeat("Zx9", 8)) + `\"}`,
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "escaped header value after an unescaped name",
		in:   `x-api-key: \"` + fake(strings.Repeat("Zx9", 8)) + `\" and then some`,
		leak: strings.Repeat("Zx9", 8),
		keep: "and then some",
	},
	{
		name: "header inside a json string value",
		in: `{"severity":"ERROR","message":"Authorization: Bearer ` + fake(strings.Repeat("A1b2C3d4", 3)) +
			`","step":"terraform apply"}`,
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: `","step":"terraform apply"}`,
	},
	{
		name: "plain header value followed by a quote",
		in:   `Authorization: Bearer ` + fake(strings.Repeat("A1b2C3d4", 3)) + `, retrying "get token" step`,
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: `"get token" step`,
	},
	{
		name: "quoted token after the scheme word",
		in:   `Authorization: Bearer "` + fake(strings.Repeat("A1b2C3d4", 3)) + `"`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "quoted token after the scheme word that is never closed",
		in:   `Authorization: Bearer "` + fake(strings.Repeat("A1b2C3d4", 3)),
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "single quoted header value",
		in:   `x-api-key: '` + fake(strings.Repeat("Zx9", 8)) + `'`,
		leak: strings.Repeat("Zx9", 8),
	},
	{
		name: "single quoted token after the scheme word",
		in:   `Authorization: Bearer '` + fake(strings.Repeat("A1b2C3d4", 3)) + `' and then some`,
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: " and then some",
	},
	{
		name: "header in single quotes over a shell command",
		in: `curl -H 'Authorization: Bearer ` + fake(strings.Repeat("A1b2C3d4", 3)) +
			`' https://api.example.com failed`,
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: `' https://api.example.com failed`,
	},
	{
		name: "quoted token after the scheme word inside a record",
		in: `{"message":"Authorization: Bearer \"` + fake(strings.Repeat("A1b2C3d4", 3)) +
			`\"","step":"terraform apply"}`,
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: `","step":"terraform apply"}`,
	},
	{
		name: "token scheme with a quoted parameter",
		in:   `Authorization: Token token="` + fake(strings.Repeat("A1b2C3d4", 3)) + `"`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "oauth 1.0 parameters in the header",
		in: `Authorization: OAuth oauth_consumer_key="abc", oauth_signature="` +
			fake(strings.Repeat("A1b2C3d4", 3)) + `"`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "token scheme with a single quoted parameter",
		in:   `Authorization: Token token='` + fake(strings.Repeat("A1b2C3d4", 3)) + `'`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "token scheme with space around the parameter sign",
		in:   `Authorization: Token token = "` + fake(strings.Repeat("A1b2C3d4", 3)) + `"`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "single quoted oauth signature",
		in: `Authorization: OAuth oauth_consumer_key='abc', oauth_signature='` +
			fake(strings.Repeat("A1b2C3d4", 3)) + `'`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "digest parameters in the header",
		in: `Authorization: Digest username="u", response="` +
			fake(strings.Repeat("A1b2C3d4", 3)) + `"`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "digest response behind an unquoted parameter",
		in: `Authorization: Digest username=u, response="` +
			fake(strings.Repeat("A1b2C3d4", 3)) + `"`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "quoted parameter inside a record",
		in: `{"message":"Authorization: Token token=\"` + fake(strings.Repeat("A1b2C3d4", 3)) +
			`\"","step":"terraform apply"}`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "escaped token after the scheme word that is never closed",
		in:   `{"message":"Authorization: Bearer \"` + fake(strings.Repeat("A1b2C3d4", 3)),
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "escaped token after the scheme word closed by a bare quote",
		in:   `{"message":"Authorization: Bearer \"` + fake(strings.Repeat("A1b2C3d4", 3)) + `","step":"terraform apply"}`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "header over a nested object",
		in: `{"authorization":{"token":"` + fake(strings.Repeat("A1b2C3d4", 3)) +
			`"},"step":"terraform apply"}`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "credential beside a nested object",
		in: `{"authorization":{"a":{"b":1},"token":"` + fake(strings.Repeat("A1b2C3d4", 3)) +
			`"}}`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "header over a list holding quotes",
		in:   `authorization: ["` + fake(strings.Repeat("A1b2C3d4", 3)) + `"]`,
		leak: strings.Repeat("A1b2C3d4", 3),
	},
	{
		name: "header over a list of plain values",
		in:   `authorization: [1, 2] and the rest of the line`,
		leak: "[1, 2]",
		keep: " and the rest of the line",
	},
	{
		name: "header quoted inside a shell command",
		in:   `curl -H "x-api-key: ` + fake(strings.Repeat("Zx9", 8)) + `" https://api.example.com`,
		leak: strings.Repeat("Zx9", 8),
		keep: " https://api.example.com",
	},
	{
		name: "bearer without a header name",
		in:   "retrying with Bearer " + fake(strings.Repeat("A1b2C3d4", 3)) + " now",
		leak: strings.Repeat("A1b2C3d4", 3),
		keep: "now",
	},
	{
		name: "signed url",
		in:   "https://b.s3.amazonaws.com/o?X-Amz-Signature=" + fake(strings.Repeat("ab", 16)) + "&X-Amz-Expires=3600",
		leak: strings.Repeat("ab", 16),
		keep: "X-Amz-Expires=3600",
	},
	{
		name: "google signed url",
		in:   "https://storage.googleapis.com/o?X-Goog-Signature=" + fake(strings.Repeat("cd", 16)) + "&X-Goog-Expires=60",
		leak: strings.Repeat("cd", 16),
		keep: "X-Goog-Expires=60",
	},
	{
		name: "token query parameter",
		in:   "https://host/cb?access_token=" + fake(strings.Repeat("ef", 16)) + "&state=ok",
		leak: strings.Repeat("ef", 16),
		keep: "state=ok",
	},
	{
		name: "token in a url fragment",
		in:   "redirect to https://app.example.com/cb#access_token=" + fake(strings.Repeat("ef", 16)) + "&state=ok",
		leak: strings.Repeat("ef", 16),
		keep: "state=ok",
	},
	{
		name: "private key block",
		in: fake("-----BEGIN ", "RSA PRIVATE KEY-----\n",
			strings.Repeat("b2R5LW1hdGVyaWFs", 4), "\n-----END ", "RSA PRIVATE KEY-----") + "\nafter",
		leak: strings.Repeat("b2R5LW1hdGVyaWFs", 4),
		keep: "after",
	},
	{
		name: "openssh key block",
		in:   fake("-----BEGIN ", "OPENSSH PRIVATE KEY-----\n", "c2VjcmV0", "\n-----END ", "OPENSSH PRIVATE KEY-----"),
		leak: "c2VjcmV0",
	},
	{
		name: "unterminated key block",
		in:   fake("-----BEGIN ", "PRIVATE KEY-----\n", "dW50ZXJtaW5hdGVk"),
		leak: "dW50ZXJtaW5hdGVk",
	},
	{
		name: "password in an environment dump",
		in:   "PGPASSWORD=" + fake(strings.Repeat("Qw3", 5)) + " psql: connection refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "psql: connection refused",
	},
	{
		name: "password field of a json record",
		in: `{"db_password":"` + fake(strings.Repeat("Qw3", 5)) +
			`","message":"quota exceeded","step":"terraform apply"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `"step":"terraform apply"}`,
	},
	{
		name: "password escaped inside a json string",
		in:   `{"content":"{\"password\":\"` + fake(strings.Repeat("Qw3", 5)) + `\"}"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `\"}"}`,
	},
	{
		name: "password field of an escaped json record",
		in: `{\"password\":\"` + fake(strings.Repeat("Qw3", 5)) +
			`\",\"step\":\"terraform apply\"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `\",\"step\":\"terraform apply\"}`,
	},
	{
		name: "camel case password field of an escaped json record",
		in: `{\"dbPassword\":\"` + fake(strings.Repeat("Qw3", 5)) +
			`\",\"step\":\"terraform apply\"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `\",\"step\":\"terraform apply\"}`,
	},
	{
		name: "password field inside a json string value",
		in: `{"message":"invalid token: ` + fake(strings.Repeat("Qw3", 5)) +
			`","type":"auth"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `","type":"auth"}`,
	},
	{
		name: "password beginning with a bracket",
		in:   "PGPASSWORD=" + fake("[", strings.Repeat("Qw3", 5)) + " psql: connection refused",
		leak: fake("[", strings.Repeat("Qw3", 5)),
		keep: "psql: connection refused",
	},
	{
		name: "password beginning with a bracket in a record",
		in: `{"password":"` + fake("[", strings.Repeat("Qw3", 5)) +
			`","step":"terraform apply"}`,
		leak: fake("[", strings.Repeat("Qw3", 5)),
		keep: `","step":"terraform apply"}`,
	},
	{
		name: "password on a command line",
		in:   "--password " + fake(strings.Repeat("Qw3", 5)) + " --user someone failed",
		leak: strings.Repeat("Qw3", 5),
		keep: "--user someone failed",
	},
	{
		name: "password of a connection string",
		in: "postgres://app:" + fake(strings.Repeat("Qw3", 5)) +
			"@db.example.com:5432/app refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "db.example.com:5432/app refused",
	},
	{
		name: "connection string with no user",
		in:   "redis://:" + fake(strings.Repeat("Qw3", 5)) + "@cache.example.com:6379 timed out",
		leak: strings.Repeat("Qw3", 5),
		keep: "cache.example.com:6379 timed out",
	},
	{
		name: "password field of a keyword connection string",
		in: "Server=db.example.com;User Id=app;Pwd=" + fake(strings.Repeat("Qw3", 5)) +
			";Database=app",
		leak: strings.Repeat("Qw3", 5),
		keep: "Database=app",
	},
	{
		name: "secret key in an environment dump",
		in:   "AWS_SECRET_ACCESS_KEY=" + fake(strings.Repeat("Qw3", 5)) + " aws s3 refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "aws s3 refused",
	},
	{
		name: "password field of a record in camel case",
		in: `{"dbPassword":"` + fake(strings.Repeat("Qw3", 5)) +
			`","step":"terraform apply"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `"step":"terraform apply"}`,
	},
	{
		name: "secret key a camel-case name introduces",
		in:   "smtpSecretKey=" + fake(strings.Repeat("Qw3", 5)) + " smtp refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "smtp refused",
	},
	{
		name: "password in single quotes",
		in:   "PGPASSWORD='" + fake(strings.Repeat("Qw3", 5)) + "' psql: connection refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "psql: connection refused",
	},
	{
		name: "password field of a record in single quotes",
		in:   `{'password': '` + fake(strings.Repeat("Qw3", 5)) + `'}`,
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "password field of a camel-case record in single quotes",
		in:   `{'dbPassword': '` + fake(strings.Repeat("Qw3", 5)) + `'}`,
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "password on a command line in single quotes",
		in:   "--password '" + fake(strings.Repeat("Qw3", 5)) + "' --user someone failed",
		leak: strings.Repeat("Qw3", 5),
		keep: "--user someone failed",
	},
	{
		name: "password holding a colon in a connection string",
		in: "postgres://app:" + fake("pa", ":", strings.Repeat("Qw3", 5)) +
			"@db.example.com:5432/app refused",
		leak: "pa" + ":" + strings.Repeat("Qw3", 5),
		keep: "db.example.com:5432/app refused",
	},
	{
		name: "password with a comma inside quotes",
		in:   `{"password":"` + fake("ab,", "cdefgh12") + `"}`,
		leak: fake("ab,", "cdefgh12"),
	},
	{
		name: "password with a semicolon inside quotes",
		in:   `{"password":"` + fake("Xy;", "9kLmnop") + `"}`,
		leak: fake("Xy;", "9kLmnop"),
	},
	{
		name: "password of several words inside quotes",
		in:   `PASSWORD="` + fake("my long ", "pass phrase") + `"`,
		leak: fake("my long ", "pass phrase"),
	},
	{
		name: "password of several words on a command line",
		in:   `--password "` + fake("my long ", "pass phrase") + `" --user someone failed`,
		leak: fake("my long ", "pass phrase"),
		keep: "--user someone failed",
	},
	{
		name: "bare password name in an environment dump",
		in:   "PASS=" + fake(strings.Repeat("Qw3", 5)) + " psql: connection refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "psql: connection refused",
	},
	{
		name: "bare secret name in an environment dump",
		in:   "JWT_SECRET=" + fake(strings.Repeat("Qw3", 5)) + " the token is refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "the token is refused",
	},
	{
		name: "password field of a struct dump",
		in:   "{User:app Password:" + fake(strings.Repeat("Qw3", 5)) + " Host:db} refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "Host:db} refused",
	},
	{
		name: "password field after a colon with text after it",
		in:   "smtpPassword: " + fake(strings.Repeat("Qw3", 5)) + " rejected by the server",
		leak: strings.Repeat("Qw3", 5),
		keep: "rejected by the server",
	},
	{
		name: "password holding a hash in a connection string",
		in:   "postgres://app:" + fake("ab#", "cd1234XYZ") + "@db.example.com:5432/app refused",
		leak: fake("ab#", "cd1234XYZ"),
		keep: "db.example.com:5432/app refused",
	},
	{
		name: "password holding a question mark in a connection string",
		in:   "postgres://app:" + fake("ab?", "cd1234XYZ") + "@db.example.com:5432/app refused",
		leak: fake("ab?", "cd1234XYZ"),
		keep: "db.example.com:5432/app refused",
	},
	{
		name: "password holding a slash in a connection string",
		in:   "postgres://app:" + fake("ab/", "cd1234XYZ") + "@db.example.com:5432/app refused",
		leak: fake("ab/", "cd1234XYZ"),
		keep: "db.example.com:5432/app refused",
	},
	{
		name: "password holding an at sign in a connection string",
		in:   "postgres://app:" + fake("p@", "ss1234XYZ") + "@db.example.com:5432/app refused",
		leak: fake("ss1234XYZ"),
		keep: "db.example.com:5432/app refused",
	},
	{
		name: "password with a single quote inside double quotes",
		in:   `{"password":"` + fake("ab'", "cdefgh12") + `"}`,
		leak: fake("ab'", "cdefgh12"),
	},
	{
		name: "password with a double quote inside single quotes",
		in:   `{'password': '` + fake(`ab"`, "cdefgh12") + `'}`,
		leak: fake(`ab"`, "cdefgh12"),
	},
	{
		name: "password with an escaped quote inside double quotes",
		in:   `{"password":"` + fake(`ab\"`, "cdefgh12") + `"}`,
		leak: fake(`ab\"`, "cdefgh12"),
	},
	{
		name: "password with an escaped quote after four bytes",
		in:   `{"password":"` + fake("abcd", `\"`, "ijk45678") + `"}`,
		leak: fake("ijk45678"),
	},
	{
		name: "password with a single quote after an equals sign",
		in:   `password="` + fake("abcd'", "lmn90123") + `"`,
		leak: fake("lmn90123"),
	},
	{
		name: "client secret field of a record after a colon",
		in:   `{"client_secret":"` + fake(strings.Repeat("Qw3", 5)) + `"}`,
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "bare secret field of a record after a colon",
		in:   `{"secret": "` + fake(strings.Repeat("Qw3", 5)) + `"}`,
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "client secret field of a yaml record",
		in:   "client_secret: " + fake(strings.Repeat("Qw3", 5)) + " refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "refused",
	},
	{
		name: "password name in capitals after a colon",
		in:   "DB_PASS: " + fake(strings.Repeat("Qw3", 5)) + " psql: connection refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "psql: connection refused",
	},
	{
		name: "secret name in capitals after a colon",
		in:   "JWT_SECRET: " + fake(strings.Repeat("Qw3", 5)) + " the token is refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "the token is refused",
	},
	{
		name: "auth token in an environment dump",
		in:   "SENTRY_AUTH_TOKEN=" + fake(strings.Repeat("Qw3", 5)) + " sentry refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "sentry refused",
	},
	{
		name: "api key in an environment dump",
		in:   "DD_API_KEY=" + fake(strings.Repeat("Qw3", 5)) + " dd refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "dd refused",
	},
	{
		name: "access key in an environment dump",
		in:   "ACCESS_KEY=" + fake(strings.Repeat("Qw3", 5)) + " s3 refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "s3 refused",
	},
	{
		name: "token in an environment dump",
		in:   "token=" + fake(strings.Repeat("Qw3", 5)) + " the token is refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "the token is refused",
	},
	{
		name: "api key field of a record in camel case",
		in: `{"apiKey":"` + fake(strings.Repeat("Qw3", 5)) +
			`","step":"terraform apply"}`,
		leak: strings.Repeat("Qw3", 5),
		keep: `"step":"terraform apply"}`,
	},
	{
		name: "token on a command line",
		in:   "--token " + fake(strings.Repeat("Qw3", 5)) + " --user someone failed",
		leak: strings.Repeat("Qw3", 5),
		keep: "--user someone failed",
	},
	{
		name: "password with a comma outside quotes",
		in:   "PGPASSWORD=" + fake("Qw3Qw,", "3Qw3Qw3x") + " psql: connection refused",
		leak: fake(",", "3Qw3Qw3x"),
		keep: "psql: connection refused",
	},
	{
		name: "password with a backslash outside quotes",
		in:   "PGPASSWORD=" + fake(`Qw3Qw\`, "w3Qw3Qw3") + " psql: connection refused",
		leak: fake(`\`, "w3Qw3Qw3"),
		keep: "psql: connection refused",
	},
	{
		name: "password with a semicolon before its fourth byte",
		in:   "PGPASSWORD=" + fake("ab;", "cdefgh12") + " psql: connection refused",
		leak: fake("ab;", "cdefgh12"),
		keep: "psql: connection refused",
	},
	{
		name: "password field followed by another password field",
		in:   "Pwd=" + fake(strings.Repeat("Qw3", 5)) + ";Pwd=" + fake(strings.Repeat("Qw3", 5)),
		leak: strings.Repeat("Qw3", 5),
	},
	{
		name: "password of a mysql dsn",
		in:   "app:" + fake(strings.Repeat("Qw3", 5)) + "@tcp(db:3306)/app refused",
		leak: strings.Repeat("Qw3", 5),
		keep: "db:3306)/app refused",
	},
	{
		name: "password with a single quote in a connection string",
		in:   "postgres://app:" + fake("ab'", "cdefgh12") + "@db.example.com/app refused",
		leak: fake("ab'", "cdefgh12"),
		keep: "db.example.com/app refused",
	},
	{
		name: "password cut off after the other kind of quote",
		in:   `{\"password\":\"` + fake("ab'", "cdefgh12"),
		leak: fake("ab'", "cdefgh12"),
	},
	{
		name: "password holding a single quote outside quotes",
		in:   "PGPASSWORD=" + fake("it's-a-", "secret-value") + " psql: connection refused",
		leak: fake("it's-a-", "secret-value"),
		keep: "psql: connection refused",
	},
	{
		name: "password holding a double quote outside quotes",
		in:   "PGPASSWORD=" + fake(`abcd"`, "efgh1234") + " psql: connection refused",
		leak: fake(`"`, "efgh1234"),
		keep: "psql: connection refused",
	},
	{
		name: "password beginning with a comma",
		in:   "PGPASSWORD=" + fake(",", "Qw3Qw3Qw3x") + " psql: connection refused",
		leak: fake("Qw3Qw3Qw3x"),
		keep: "psql: connection refused",
	},
	{
		name: "password beginning with a backslash",
		in:   "PGPASSWORD=" + fake(`\`, "srv-pw-1234") + " psql: connection refused",
		leak: fake(`\`, "srv-pw-1234"),
		keep: "psql: connection refused",
	},
}

// TestRedactCorpus: a shape the exact pass cannot name is still a credential,
// and Text removes it while leaving the rest of the line alone. One row per
// kind, every value built from fragments.
func TestRedactCorpus(t *testing.T) {
	for _, tc := range patternCases {
		t.Run(tc.name, func(t *testing.T) {
			got := Text(tc.in, nil)
			if strings.Contains(got, tc.leak) {
				t.Errorf("Text(%q) = %q: %q survived the pattern pass", tc.in, got, tc.leak)
			}
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("Text(%q) = %q: nothing was redacted", tc.in, got)
			}
			if tc.keep != "" && !strings.Contains(got, tc.keep) {
				t.Errorf("Text(%q) = %q: the pattern pass ate %q as well", tc.in, got, tc.keep)
			}
		})
	}
}

// TestTextIsBothPasses: Text is the exact pass and then the pattern pass, so
// one string holding a value Fugaro holds and one it does not loses both.
func TestRedactsBothPasses(t *testing.T) {
	known := fake("known-", "value-0123456789")
	shaped := fake("ghp_", strings.Repeat("A1b2C3d4", 5))
	got := Text("line "+known+" and "+shaped+" end", []string{known})
	if strings.Contains(got, known) || strings.Contains(got, shaped) {
		t.Errorf("Text = %q: one of the two passes did not run", got)
	}
	if got != "line [REDACTED] and [REDACTED] end" {
		t.Errorf("Text = %q, want both matches replaced", got)
	}
}

// TestReportMasksThePerson: the issue report is the one output that must not
// carry the address or the home directory of whoever filed it.
func TestReportRedactsThePerson(t *testing.T) {
	for _, tc := range []struct{ name, in, leak string }{
		{"email", "filed by " + fake("dimi", "@", "example.com") + " today", fake("dimi", "@", "example.com")},
		{"home path", "at " + fake("/home/", "someone", "/.config/fugaro/fugaro.yaml") + " the file is", fake("/home/", "someone")},
		{"home path in a file url", "at " + fake("file:///home/", "someone", "/x.yaml") + " it is", fake("/home/", "someone")},
		{"home path under var", "at " + fake("/var/home/", "someone", "/x.yaml") + " it is", fake("/var/home/", "someone")},
		{"mac home path", "at " + fake("/Users/", "someone", "/Library/logs") + " it is", fake("/Users/", "someone")},
		{"root home", "reading " + fake("/root", "/.config/fugaro") + " now", fake("/root", "/.config/fugaro")},
		{"windows home path", "at " + fake(`C:\\Users\\`, "someone", `\\AppData`) + " it is", fake(`C:\\Users\\`, "someone")},
		{"home path after a compiler flag", "cc " + fake("-I/home/", "someone", "/include") + " -o out", fake("/home/", "someone")},
		{"wsl home path", "at " + fake("/mnt/c/Users/", "someone", "/x") + " it is", fake("/Users/", "someone")},
		{"git bash home path", "at " + fake("/c/Users/", "someone", "/x") + " it is", fake("/Users/", "someone")},
		{"windows home path with a space in the profile name", "at " + fake(`C:\Users\John `, "Smith") + `\AppData it is`, "Smith"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Report(tc.in)
			if strings.Contains(got, tc.leak) {
				t.Errorf("Report(%q) = %q: %q survived", tc.in, got, tc.leak)
			}
		})
	}
}

// TestRedactKeepsAStructuredRecordReadable: a structured log is a record of
// fields, and the fields around the credential are the diagnosis. A header
// rule that ran to the end of the line would leave the record unparseable and
// the message gone.
func TestRedactKeepsAStructuredRecordReadable(t *testing.T) {
	token := fake(strings.Repeat("A1b2C3d4", 3))
	in := `{"severity":"ERROR","authorization":"Bearer ` + token +
		`","message":"quota exceeded for project foo","step":"terraform apply"}`
	got := Text(in, nil)
	if strings.Contains(got, token) {
		t.Errorf("Text = %q: the token survived", got)
	}
	var rec map[string]string
	if err := json.Unmarshal([]byte(got), &rec); err != nil {
		t.Fatalf("Text = %q, which is no longer a JSON record: %v", got, err)
	}
	for field, want := range map[string]string{
		"severity": "ERROR",
		"message":  "quota exceeded for project foo",
		"step":     "terraform apply",
	} {
		if rec[field] != want {
			t.Errorf("Text lost %s: %#v", field, rec)
		}
	}
	if rec["authorization"] != "[REDACTED]" {
		t.Errorf("authorization = %q, want the marker", rec["authorization"])
	}
}

// TestRedactKeepsAnEscapedRecordReadable: the same record one encoding in, as
// a transcript field or a provider error body carries it — a JSON object
// inside a JSON string, where every quote of the inner object is escaped. The
// pass has to reach the header through that escaping, and both records have to
// still parse afterwards.
func TestRedactKeepsAnEscapedRecordReadable(t *testing.T) {
	token := fake(strings.Repeat("A1b2C3d4", 3))
	inner := `{"severity":"ERROR","authorization":"Bearer ` + token +
		`","message":"quota exceeded for project foo","step":"terraform apply"}`
	got := Text(`{"type":"result","content":`+strconv.Quote(inner)+`}`, nil)
	if strings.Contains(got, token) {
		t.Fatalf("Text = %q: the token survived the escaping", got)
	}
	var outer struct{ Content string }
	if err := json.Unmarshal([]byte(got), &outer); err != nil {
		t.Fatalf("Text = %q, which is no longer a JSON record: %v", got, err)
	}
	var rec map[string]string
	if err := json.Unmarshal([]byte(outer.Content), &rec); err != nil {
		t.Fatalf("the record inside it is %q, which no longer parses: %v", outer.Content, err)
	}
	for field, want := range map[string]string{
		"severity": "ERROR",
		"message":  "quota exceeded for project foo",
		"step":     "terraform apply",
	} {
		if rec[field] != want {
			t.Errorf("Text lost %s: %#v", field, rec)
		}
	}
	if rec["authorization"] != "[REDACTED]" {
		t.Errorf("authorization = %q, want the marker", rec["authorization"])
	}
}

// TestRedactKeepsAPasswordRecordReadable: the same two records read by the name
// of the field rather than by a scheme word. A record's fields are the
// diagnosis, and a password rule that read a value past the quote closing it —
// the quote of the field in a plain record, the `\"` of one in an escaped
// record — would take the fields after the credential and leave a text that is
// no longer a record at all.
func TestRedactKeepsAPasswordRecordReadable(t *testing.T) {
	value := fake(strings.Repeat("Qw3", 5))
	check := func(t *testing.T, got string) {
		t.Helper()
		if strings.Contains(got, value) {
			t.Errorf("Text = %q: the value survived", got)
		}
		var rec map[string]string
		if err := json.Unmarshal([]byte(got), &rec); err != nil {
			t.Fatalf("Text = %q, which is no longer a JSON record: %v", got, err)
		}
		for field, want := range map[string]string{
			"severity": "ERROR",
			"message":  "quota exceeded for project foo",
			"step":     "terraform apply",
		} {
			if rec[field] != want {
				t.Errorf("Text lost %s: %#v", field, rec)
			}
		}
		if rec["token"] != "[REDACTED]" {
			t.Errorf("token = %q, want the marker", rec["token"])
		}
	}
	inner := `{"severity":"ERROR","token":"` + value +
		`","message":"quota exceeded for project foo","step":"terraform apply"}`

	t.Run("plain", func(t *testing.T) {
		check(t, Text(inner, nil))
	})

	t.Run("escaped", func(t *testing.T) {
		got := Text(`{"type":"result","content":`+strconv.Quote(inner)+`}`, nil)
		if strings.Contains(got, value) {
			t.Fatalf("Text = %q: the value survived the escaping", got)
		}
		var outer struct{ Content string }
		if err := json.Unmarshal([]byte(got), &outer); err != nil {
			t.Fatalf("Text = %q, which is no longer a JSON record: %v", got, err)
		}
		check(t, outer.Content)
	})
}

// TestRedactReportIsIdempotentWhenAMaskBites: this is the shape that made the
// report change under a second application — a home path inside a quoted
// value that is never closed, where masking the path is what closes the value
// and lets the pattern pass see a match it had run past. The masks run before
// the pass, so Report is idempotent here whatever the mask removes.
func TestRedactReportIsIdempotentWhenAMaskBites(t *testing.T) {
	in := `authorization: "` + fake("eyJsig=", `\"/root\"`)
	once := Report(in)
	if strings.Contains(once, fake("/root")) {
		t.Errorf("Report(%q) = %q: the home path survived", in, once)
	}
	if twice := Report(once); twice != once {
		t.Errorf("Report is not idempotent: once %q, twice %q", once, twice)
	}
}

// TestRedactKeepsARecordReadableWhenTheHeaderIsInsideAValue: a record may
// quote a whole command or an upstream error inside one field, so the header
// sits inside a string value rather than at the top level, with the record's
// remaining fields after it. Those fields are the diagnosis: a rule that ran
// to the end of the line would take them and leave text that no longer parses.
func TestRedactKeepsARecordReadableWhenTheHeaderIsInsideAValue(t *testing.T) {
	token := fake(strings.Repeat("A1b2C3d4", 3))
	in := `{"severity":"ERROR","message":"curl -H 'Authorization: Bearer ` + token +
		`' https://api.example.com failed","step":"terraform apply"}`
	got := Text(in, nil)
	if strings.Contains(got, token) {
		t.Errorf("Text = %q: the token survived", got)
	}
	var rec map[string]string
	if err := json.Unmarshal([]byte(got), &rec); err != nil {
		t.Fatalf("Text = %q, which is no longer a JSON record: %v", got, err)
	}
	for field, want := range map[string]string{
		"severity": "ERROR",
		"step":     "terraform apply",
	} {
		if rec[field] != want {
			t.Errorf("Text lost %s: %#v", field, rec)
		}
	}
	if !strings.Contains(rec["message"], "[REDACTED]") {
		t.Errorf("message = %q, want the marker", rec["message"])
	}
}

// TestRedactReportMasksAPathThePassUncovered: the masks run over what the
// pattern pass produced as well as over what it was given. A token standing
// where a mask's delimiter has to be keeps the path from being masked on the
// text as it arrived, and the pass takes that delimiter away along with the
// token: masked once more, the path goes, or the report names the person who
// filed it and its text changes under a second application.
func TestRedactReportMasksAPathThePassUncovered(t *testing.T) {
	in := "at " + fake("ghp_", strings.Repeat("A1b2C3d4", 5)) + fake("/home/", "someone", "/.cache")
	once := Report(in)
	if strings.Contains(once, fake("/home/", "someone")) {
		t.Errorf("Report(%q) = %q: the home path survived", in, once)
	}
	if twice := Report(once); twice != once {
		t.Errorf("Report is not idempotent: once %q, twice %q", once, twice)
	}
}

// TestReportAlsoRedactsPatterns: Report is the last pass over the issue text,
// so a credential the model wrote into it does not reach the file either.
func TestReportAlsoRedactsPatterns(t *testing.T) {
	shaped := fake("ghp_", strings.Repeat("A1b2C3d4", 5))
	if got := Report("token " + shaped); strings.Contains(got, shaped) {
		t.Errorf("Report(%q) = %q: the token survived", shaped, got)
	}
}

// TestPassesLeaveCleanTextAlone: a diagnosis is mostly ordinary words, and a
// pass that redacted them would be unreadable.
func TestRedactPassesLeaveCleanTextAlone(t *testing.T) {
	for _, s := range []string{
		"the update failed with exit code 1: terraform apply",
		"see https://github.com/dimipaun/fugaro/pull/1918 for the change",
		"retrying in 30s, attempt 3 of 5",
		"the task-sk-1234567890abcdefghij note is not a key",
		"see https://example.com/home/page for the shape",
		"https://host/cb?token=&state=ok returned nothing",
		"retrying with Bearer\nQWERTYUIOPASDFGHJKLZXCVBNM now",
		"the password is wrong, so the update stops",
		"the password's wrong, so the update stops",
		"the secret: it was rotated by the operator",
		"compass: north, and bypass the cache to surpass it",
		"a Compass: north, and the Surpass: 3 points",
		"--- PASS: TestRunnerFinalize (0.00s)",
		"Pass: TestRunnerFinalize (0.00s)",
		"the secret: rotated by the operator",
		"the pass: it was accepted by the runner",
		"the word 'pass' in quotes: it is not a field",
		"see https://api.example.com:8080/v1/runs for the endpoint",
	} {
		if got := Text(s, []string{"a-secret-value"}); got != s {
			t.Errorf("Text(%q) = %q, want it unchanged", s, got)
		}
		if got := Report(s); got != s {
			t.Errorf("Report(%q) = %q, want it unchanged", s, got)
		}
	}
}

// TestPassesAreIdempotent: a diagnosis is redacted again on its way into the
// report, and a second pass must not keep eating the text.
func TestRedactPassesAreIdempotent(t *testing.T) {
	for _, tc := range patternCases {
		exact := []string{"a-known-value", "another-known-value"}
		once := Text(tc.in, exact)
		if twice := Text(once, exact); twice != once {
			t.Errorf("Text is not idempotent for %s:\n once: %q\ntwice: %q", tc.name, once, twice)
		}
		reported := Report(once)
		if again := Report(reported); again != reported {
			t.Errorf("Report is not idempotent for %s:\n once: %q\ntwice: %q", tc.name, reported, again)
		}
	}
}

// TestPassesAreBounded: both passes run over a whole log, so their cost has
// to be linear in the text rather than in what the text repeats. The bound is
// loose on purpose — this runs under -race on a shared machine, where the
// same pass costs an order of magnitude more — and it is still far below what
// a pass that scanned the text again for every position would take.
func TestRedactPassesAreBounded(t *testing.T) {
	token := fake(strings.Repeat("A1b2C3d4", 3))
	sig := fake(strings.Repeat("ab", 16))
	key := fake("-----BEGIN ", "RSA PRIVATE KEY-----\n", strings.Repeat("b2R5", 8), "\n-----END ", "RSA PRIVATE KEY-----")
	jwt := fake("eyJ", "hbGciOiJIUzI1NiJ9.", "eyJzdWIiOiIxIn0.", "c2lnbmF0dXJlMTIz")
	person := fake("dimi", "@", "example.com")
	home := fake("/home/", "someone")
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("Authorization: Bearer " + token + "\n")
		b.WriteString(key + "\n")
		b.WriteString("?sig=" + sig + "&X-Amz-Expires=1\n")
		b.WriteString(jwt + "\n")
		b.WriteString("reached " + person + " at " + home + "/.config\n")
	}
	in := b.String()
	if len(in) < 1<<17 {
		t.Fatalf("the fixture is %d bytes, too small to say anything about cost", len(in))
	}
	start := time.Now()
	got := Text(in, []string{"a-known-value"})
	got = Report(got)
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("redacting %d bytes took %s, which is not a pass over a log", len(in), d)
	}
	for _, leak := range []string{person, home, token, sig} {
		if strings.Contains(got, leak) {
			t.Errorf("%q survived a pass over %d bytes", leak, len(in))
		}
	}
}
