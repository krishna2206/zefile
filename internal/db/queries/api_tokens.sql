-- name: GetAPITokenByHash :one
-- The lookup performed on every request that arrives with a zefile_live_ token.
-- Expiry and revocation are filtered here, alongside the account being enabled,
-- so a caller cannot present a token whose owner has been disabled. A NULL
-- expires_at means the token never expires.
SELECT sqlc.embed(api_tokens), sqlc.embed(users)
FROM api_tokens
JOIN users ON users.id = api_tokens.user_id
WHERE api_tokens.token_hash = ?
  AND api_tokens.revoked_at IS NULL
  AND (api_tokens.expires_at IS NULL OR api_tokens.expires_at > ?)
  AND users.disabled = 0;

-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash, prefix, scopes, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAPITokensForUser :many
-- The management list. Revoked tokens are dropped so the screen shows only what
-- is still live; the plaintext is never held, so only the prefix is shown.
SELECT * FROM api_tokens
WHERE user_id = ? AND revoked_at IS NULL
ORDER BY created_at DESC;

-- name: RevokeAPITokenForUser :execrows
-- Scoped to the owner so one account cannot revoke another's token by guessing
-- an id. execrows lets the handler answer 404 when nothing matched.
UPDATE api_tokens SET revoked_at = ?
WHERE id = ? AND user_id = ? AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
-- Records use for the "last used" column. Best-effort, like session touch: a
-- lost timestamp must never fail an otherwise-valid request.
UPDATE api_tokens SET last_used_at = ? WHERE id = ?;
