---
name: sync-docs
description: Synchronize documentation with current code logic. Use when documentation may be outdated, after code changes that affect documented behavior, or when the user asks to update/review docs.
---

# Sync Documentation with Code

## Purpose

Keep all project documentation consistent with the actual code implementation. This includes README files, technical docs, AGENTS.md, CONTRIBUTING guides, and any other markdown documentation. The skill analyzes the current codebase and updates documentation to reflect the true behavior, APIs, configurations, and workflows.

## When to Use

- After significant code changes that affect documented features
- When reviewing documentation for accuracy
- When the user mentions docs are outdated or asks to sync docs
- Before releases to ensure documentation matches shipped code
- When adding new features that need documentation
- When refactoring code that changes public interfaces

## Instructions

### 1. Discover All Documentation

Scan the entire project for documentation files:

- Root-level docs: `README.md`, `README-*.md`, `AGENTS.md`, `CONTRIBUTING.md`, `CHANGELOG.md`
- Technical docs: `docs/**/*.md`
- Inline docs: `**/plan.md`, `**/understanding.md`
- Prompt templates: `prompts/**/*.md`
- Build guides: `**/BUILD-GUIDE.md`

Read each document to understand its purpose and scope.

### 2. Map Documentation to Code

For each documentation file, identify which code modules, configs, or features it describes:

- README files → project structure, installation, usage, CLI commands
- Technical docs → specific modules in `internal/`, `cmd/`, `pkg/`
- Config docs → `configs/*.yaml` and config parsing code
- Prompt docs → `prompts/` directory structure and templating logic
- Build guides → build scripts, Makefile targets, dependencies

### 3. Analyze Code and Compare

For each doc-to-code mapping:

1. **Read the documentation** to understand what it claims
2. **Read the corresponding source code** to understand actual behavior
3. **Identify discrepancies**:
   - Changed function signatures, parameters, return types
   - New or removed configuration options
   - Modified workflow steps or decision logic
   - Updated dependencies or build requirements
   - Changed CLI flags or commands
   - New features not yet documented
   - Removed features still documented

### 4. Update Documentation

Apply updates following these principles:

- **Preserve style**: Match the existing tone, formatting, and structure
- **Be accurate**: Only document what the code actually does
- **Be complete**: Add missing documentation for new features
- **Be concise**: Remove obsolete content for removed features
- **Use examples**: Update code snippets to reflect current APIs
- **Cross-reference**: Ensure links between docs remain valid

### 5. Verification Checklist

Before completing, verify:

- [ ] Code examples are syntactically correct and runnable
- [ ] Configuration examples match actual schemas
- [ ] CLI commands and flags match implementation
- [ ] API descriptions match function signatures
- [ ] Installation/build instructions work
- [ ] Internal links between docs are valid
- [ ] No references to removed/renamed entities

## Output

Provide a summary after updating:

1. **Files updated**: List all modified documentation files
2. **Key changes**: Bullet points of significant updates
3. **New documentation**: Areas where docs were added
4. **Removed content**: Obsolete sections that were cleaned up
5. **Open questions**: Ambiguities needing maintainer input

## Constraints

- Do NOT invent features that don't exist in code
- Do NOT remove sections without confirming the feature is removed
- Do NOT change documentation unrelated to code changes
- Preserve original documentation style and tone
- Mark uncertain areas with TODO comments rather than guessing
- Keep localized versions (e.g., README-zh.md) in sync with primary README
