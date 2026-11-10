#!/usr/bin/env bash
# Deteksi stack + perintah test/lint/typecheck untuk sebuah repo.
# Output: baris KEY='value' yang bisa di-`source`.
# Override: jika <git-dir>/feature-workflow/commands.env ada, file itu yang dipakai.
set -uo pipefail

ROOT="${1:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
cd "$ROOT" || exit 1

GIT_DIR="$(git rev-parse --absolute-git-dir 2>/dev/null || true)"
OVERRIDE="${GIT_DIR:+$GIT_DIR/feature-workflow/commands.env}"
if [ -n "$OVERRIDE" ] && [ -f "$OVERRIDE" ]; then
  cat "$OVERRIDE"
  exit 0
fi

STACK=()
TEST_CMD=""
LINT_CMD=""
TYPECHECK_CMD=""

has_script() { # $1 = nama script npm
  [ -f package.json ] && jq -e --arg s "$1" '.scripts[$s] // empty | select(test("no test specified") | not)' package.json >/dev/null 2>&1
}
join() { local out=""; for c in "$@"; do [ -n "$c" ] && out="${out:+$out && }$c"; done; echo "$out"; }

# --- PHP ---
if [ -f composer.json ]; then
  STACK+=("php")
  if [ -f artisan ]; then STACK+=("laravel"); TEST_CMD=$(join "$TEST_CMD" "php artisan test")
  elif [ -x vendor/bin/phpunit ] && { [ -f phpunit.xml ] || [ -f phpunit.xml.dist ]; }; then TEST_CMD=$(join "$TEST_CMD" "vendor/bin/phpunit")
  elif [ -x vendor/bin/codecept ] && [ -f codeception.yml ]; then TEST_CMD=$(join "$TEST_CMD" "vendor/bin/codecept run unit")
  fi
  grep -q '"yiisoft/yii2"' composer.json && STACK+=("yii2")
  [ -x vendor/bin/phpstan ] && LINT_CMD=$(join "$LINT_CMD" "vendor/bin/phpstan analyse --no-progress")
  [ -x vendor/bin/pint ] && LINT_CMD=$(join "$LINT_CMD" "vendor/bin/pint --test")
  [ -x vendor/bin/phpcs ] && [ -z "$LINT_CMD" ] && LINT_CMD=$(join "$LINT_CMD" "vendor/bin/phpcs")
fi

# --- Node / TypeScript ---
if [ -f package.json ]; then
  STACK+=("node")
  PM="npm run"; [ -f pnpm-lock.yaml ] && PM="pnpm run"; [ -f yarn.lock ] && PM="yarn"
  has_script test && TEST_CMD=$(join "$TEST_CMD" "$PM test")
  has_script lint && LINT_CMD=$(join "$LINT_CMD" "$PM lint")
  if has_script typecheck; then TYPECHECK_CMD=$(join "$TYPECHECK_CMD" "$PM typecheck")
  elif [ -f tsconfig.json ]; then STACK+=("typescript"); TYPECHECK_CMD=$(join "$TYPECHECK_CMD" "npx --no-install tsc --noEmit")
  fi
fi

# --- Go ---
if [ -f go.mod ]; then
  STACK+=("go")
  TEST_CMD=$(join "$TEST_CMD" "go test ./...")
  if command -v golangci-lint >/dev/null 2>&1; then LINT_CMD=$(join "$LINT_CMD" "golangci-lint run ./...")
  else LINT_CMD=$(join "$LINT_CMD" "go vet ./...")
  fi
  TYPECHECK_CMD=$(join "$TYPECHECK_CMD" "go build ./...")
fi

# --- Python ---
if [ -f pyproject.toml ] || [ -f requirements.txt ] || [ -f setup.py ]; then
  STACK+=("python")
  PY="python3"; [ -x .venv/bin/python ] && PY=".venv/bin/python"; [ -x venv/bin/python ] && PY="venv/bin/python"
  if [ -d tests ] || [ -d test ] || ls test_*.py >/dev/null 2>&1; then TEST_CMD=$(join "$TEST_CMD" "$PY -m pytest -q"); fi
  if command -v ruff >/dev/null 2>&1 || [ -x .venv/bin/ruff ]; then LINT_CMD=$(join "$LINT_CMD" "ruff check .")
  else LINT_CMD=$(join "$LINT_CMD" "$PY -m compileall -q .")
  fi
fi

[ ${#STACK[@]} -eq 0 ] && STACK=("unknown")

printf "STACK='%s'\n" "${STACK[*]}"
printf "TEST_CMD='%s'\n" "$TEST_CMD"
printf "LINT_CMD='%s'\n" "$LINT_CMD"
printf "TYPECHECK_CMD='%s'\n" "$TYPECHECK_CMD"
