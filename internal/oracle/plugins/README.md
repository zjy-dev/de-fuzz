# Oracle Plugin Example Configuration

## Using the LLM Oracle (Default)

```yaml
config:
  # ... other config
  oracle:
    type: "llm"
    options: {}
```

## Using the Crash Oracle (Simple)

For strategies where you only care about detecting crashes:

```yaml
config:
  # ... other config
  oracle:
    type: "crash"
    options: {}
```

## Future Oracle Types

### Differential Oracle

Compare outputs from multiple compiler versions or optimization levels:

```yaml
config:
  # ... other config
  oracle:
    type: "diff"
    options:
      baseline_compiler: "/path/to/gcc-12"
      test_compiler: "/path/to/gcc-13"
```

### Static Analysis Oracle

Use static analysis tools:

```yaml
config:
  # ... other config
  oracle:
    type: "static"
    options:
      tools: ["cppcheck", "clang-tidy"]
```

### Custom Oracle

User-defined oracle logic:

```yaml
config:
  # ... other config
  oracle:
    type: "custom"
    options:
      script: "/path/to/custom_oracle.sh"
```
