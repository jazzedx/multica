package main

import (
	"os"
	"strings"

	"github.com/multica-ai/multica/server/internal/handler"
)

// builtinCodeHostFromEnv reads where the bundled code host is and how the
// server reaches it. It is set on the config after the literal in router.go,
// so that literal stays formatted as upstream has it.
func builtinCodeHostFromEnv() handler.BuiltinCodeHostConfig {
	return handler.BuiltinCodeHostConfig{
		URL:            strings.TrimSpace(os.Getenv("MULTICA_BUILTIN_GITEA_URL")),
		PublicURL:      strings.TrimSpace(os.Getenv("MULTICA_BUILTIN_GITEA_PUBLIC_URL")),
		WebhookBaseURL: strings.TrimSpace(os.Getenv("MULTICA_BUILTIN_GITEA_WEBHOOK_BASE_URL")),
		Owner:          strings.TrimSpace(os.Getenv("MULTICA_BUILTIN_GITEA_OWNER")),
		Token:          strings.TrimSpace(os.Getenv("MULTICA_BUILTIN_GITEA_TOKEN")),
		TokenFile:      strings.TrimSpace(os.Getenv("MULTICA_BUILTIN_GITEA_TOKEN_FILE")),
	}
}
