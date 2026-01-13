#!/bin/bash
# LLM Stress Test Script for DeFuzz
# Simulates fuzzing by sending 8 unique prompts to test LLM response time
# Each prompt is unique to avoid caching
#
# Usage: ./scripts/llm_stress_test.sh <provider> [iterations]
#   provider:   deepseek or minimax
#   iterations: Number of LLM calls (default: 8)

set -e

# Configuration
PROVIDER=${1:-"deepseek"}
ITERATIONS=${2:-8}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

# Load environment variables
if [ -f "$PROJECT_ROOT/.env" ]; then
    set -a
    source "$PROJECT_ROOT/.env"
    set +a
fi

# Validate provider and set API configuration
case "$PROVIDER" in
    deepseek)
        API_KEY="${DEEPSEEK_API_KEY}"
        ENDPOINT="${DEEPSEEK_ENDPOINT:-https://api.deepseek.com/v1/chat/completions}"
        MODEL="deepseek-chat"
        ;;
    minimax)
        API_KEY="${MINIMAX_API_KEY}"
        ENDPOINT="${MINIMAX_ENDPOINT:-https://api.minimaxi.com/v1/text/chatcompletion_v2}"
        MODEL="MiniMax-M2.1"
        ;;
    *)
        log_error "Invalid provider: $PROVIDER"
        echo "Usage: $0 <deepseek|minimax> [iterations]"
        exit 1
        ;;
esac

if [ -z "$API_KEY" ]; then
    log_error "API key not set for provider: $PROVIDER"
    echo "Please set ${PROVIDER^^}_API_KEY in .env file"
    exit 1
fi

echo ""
log_info "============================================================"
log_info "DeFuzz LLM Stress Test"
log_info "============================================================"
log_info "Provider:    ${PROVIDER}"
log_info "Model:       ${MODEL}"
log_info "Endpoint:    ${ENDPOINT}"
log_info "Iterations:  ${ITERATIONS}"
log_info "============================================================"
echo ""

# Results tracking
TOTAL_TIME=0
SUCCESS_COUNT=0
FAIL_COUNT=0
declare -a RESPONSE_TIMES

log_step "Starting LLM stress test..."
echo ""

for i in $(seq 1 $ITERATIONS); do
    # Generate unique prompt with timestamp and random suffix to avoid caching
    TIMESTAMP=$(date +%s%N)
    RANDOM_SUFFIX=$RANDOM
    BUFFER_SIZE=$((50 + i * 10))
    
    # Build request JSON with proper escaping
    REQUEST_JSON="{\"model\": \"$MODEL\", \"messages\": [{\"role\": \"system\", \"content\": \"You are a compiler security testing expert. Generate C code that triggers specific compiler behaviors. Output only valid C code without markdown.\"}, {\"role\": \"user\", \"content\": \"Generate a C function named test_func_${i} with a local char array of size ${BUFFER_SIZE}. Include a loop that writes to the buffer. Unique ID: ${TIMESTAMP}_${RANDOM_SUFFIX}. Output only C code.\"}], \"temperature\": 0.1}"
    
    log_info "Iteration $i/$ITERATIONS..."
    
    # Time the API call
    START_TIME=$(date +%s.%N)
    
    HTTP_RESPONSE=$(curl -s -w "\n%{http_code}" \
        -X POST "$ENDPOINT" \
        -H "Authorization: Bearer $API_KEY" \
        -H "Content-Type: application/json" \
        -d "$REQUEST_JSON" \
        --max-time 120)
    
    END_TIME=$(date +%s.%N)
    
    # Extract HTTP status code (last line)
    HTTP_CODE=$(echo "$HTTP_RESPONSE" | tail -n1)
    RESPONSE_BODY=$(echo "$HTTP_RESPONSE" | sed '$d')
    
    # Calculate elapsed time
    ELAPSED=$(echo "$END_TIME - $START_TIME" | bc)
    RESPONSE_TIMES+=("$ELAPSED")
    TOTAL_TIME=$(echo "$TOTAL_TIME + $ELAPSED" | bc)
    
    if [ "$HTTP_CODE" == "200" ]; then
        SUCCESS_COUNT=$((SUCCESS_COUNT + 1))
        # Extract token usage if available
        TOKENS=$(echo "$RESPONSE_BODY" | jq -r '.usage.total_tokens // "N/A"' 2>/dev/null || echo "N/A")
        echo -e "  ${GREEN}✓${NC} Response time: ${ELAPSED}s, Tokens: ${TOKENS}"
    else
        FAIL_COUNT=$((FAIL_COUNT + 1))
        ERROR_MSG=$(echo "$RESPONSE_BODY" | jq -r '.error.message // .message // "Unknown error"' 2>/dev/null || echo "$RESPONSE_BODY")
        echo -e "  ${RED}✗${NC} Failed (HTTP $HTTP_CODE): $ERROR_MSG"
    fi
done

echo ""
log_info "============================================================"
log_info "LLM Stress Test Results - ${PROVIDER}"
log_info "============================================================"

# Calculate statistics
if [ "$SUCCESS_COUNT" -gt 0 ]; then
    AVG_TIME=$(echo "scale=3; $TOTAL_TIME / $ITERATIONS" | bc)
    
    # Calculate min/max
    MIN_TIME=$(printf '%s\n' "${RESPONSE_TIMES[@]}" | sort -n | head -1)
    MAX_TIME=$(printf '%s\n' "${RESPONSE_TIMES[@]}" | sort -n | tail -1)
    
    log_info "Provider:        ${PROVIDER}"
    log_info "Model:           ${MODEL}"
    log_info "Iterations:      ${ITERATIONS}"
    log_info "Successful:      ${SUCCESS_COUNT}"
    log_info "Failed:          ${FAIL_COUNT}"
    log_info "Total time:      ${TOTAL_TIME}s"
    log_info "Avg response:    ${AVG_TIME}s"
    log_info "Min response:    ${MIN_TIME}s"
    log_info "Max response:    ${MAX_TIME}s"
else
    log_error "All requests failed!"
    AVG_TIME="0"
    MIN_TIME="0"
    MAX_TIME="0"
fi

log_info "============================================================"

# Save results to JSON
RESULTS_FILE="${PROJECT_ROOT}/docs/llm_stress_test_results.json"
mkdir -p "$(dirname "$RESULTS_FILE")"

TIMESTAMP_ISO=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Create new result entry
NEW_RESULT="{\"provider\": \"$PROVIDER\", \"model\": \"$MODEL\", \"iterations\": $ITERATIONS, \"successful\": $SUCCESS_COUNT, \"failed\": $FAIL_COUNT, \"total_seconds\": $TOTAL_TIME, \"avg_seconds\": $AVG_TIME, \"min_seconds\": $MIN_TIME, \"max_seconds\": $MAX_TIME, \"timestamp\": \"$TIMESTAMP_ISO\"}"

if [ -f "$RESULTS_FILE" ] && [ -s "$RESULTS_FILE" ]; then
    # Append to existing results
    TMP_FILE=$(mktemp)
    jq ". + [$NEW_RESULT]" "$RESULTS_FILE" > "$TMP_FILE" 2>/dev/null && mv "$TMP_FILE" "$RESULTS_FILE"
else
    echo "[$NEW_RESULT]" | jq '.' > "$RESULTS_FILE"
fi

log_info "Results saved to: ${RESULTS_FILE}"
echo ""
log_info "Done!"
