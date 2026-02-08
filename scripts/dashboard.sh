#!/bin/bash

# =============================================================================
# LIVE DASHBOARD - Real-time monitoring for MQTT benchmark
# =============================================================================

REFRESH_INTERVAL=${1:-2}

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
NC='\033[0m'

format_number() {
    printf "%'d" $1 2>/dev/null || echo $1
}

while true; do
    clear

    # Get metrics
    LG=$(curl -s http://localhost:8090/debug/vars 2>/dev/null)
    WK=$(curl -s http://localhost:8080/stats 2>/dev/null)
    EMQX=$(curl -s -u admin:public http://localhost:18083/api/v5/stats 2>/dev/null)

    echo -e "${CYAN}╔══════════════════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${CYAN}║${WHITE}           MQTT HIGH-PERFORMANCE ARCHITECTURE DASHBOARD                   ${CYAN}║${NC}"
    echo -e "${CYAN}║${NC}                      $(date '+%Y-%m-%d %H:%M:%S')                              ${CYAN}║${NC}"
    echo -e "${CYAN}╠══════════════════════════════════════════════════════════════════════════╣${NC}"

    # LOADGEN Section
    if [ -n "$LG" ] && [ "$LG" != "{}" ]; then
        sent=$(echo "$LG" | jq -r '.sent_total // 0')
        recv=$(echo "$LG" | jq -r '.recv_match_total // 0')
        clients=$(echo "$LG" | jq -r '.active_clients // 0')
        conns=$(echo "$LG" | jq -r '.active_connections // 0')
        p50=$(echo "$LG" | jq -r '.lat_p50_us // 0')
        p95=$(echo "$LG" | jq -r '.lat_p95_us // 0')
        p99=$(echo "$LG" | jq -r '.lat_p99_us // 0')

        echo -e "${CYAN}║${NC} ${GREEN}▶ LOADGEN-V2${NC}                                                            ${CYAN}║${NC}"
        printf "${CYAN}║${NC}   Virtual Clients: ${WHITE}%-15s${NC} Connections: ${WHITE}%-10s${NC}         ${CYAN}║${NC}\n" \
            "$(format_number $clients)" "$conns"
        printf "${CYAN}║${NC}   Messages Sent:   ${WHITE}%-15s${NC} Received:    ${WHITE}%-10s${NC}         ${CYAN}║${NC}\n" \
            "$(format_number $sent)" "$(format_number $recv)"
        printf "${CYAN}║${NC}   Latency P50:     ${WHITE}%-8s${NC} μs    P95: ${WHITE}%-8s${NC} μs  P99: ${WHITE}%-8s${NC} μs ${CYAN}║${NC}\n" \
            "$(format_number $p50)" "$(format_number $p95)" "$(format_number $p99)"
    else
        echo -e "${CYAN}║${NC} ${RED}▶ LOADGEN-V2: Not connected${NC}                                            ${CYAN}║${NC}"
    fi

    echo -e "${CYAN}╠══════════════════════════════════════════════════════════════════════════╣${NC}"

    # WORKER Section
    if [ -n "$WK" ] && [ "$WK" != "{}" ]; then
        proc=$(echo "$WK" | jq -r '.processed // 0')
        recv_w=$(echo "$WK" | jq -r '.received // 0')
        pub=$(echo "$WK" | jq -r '.published // 0')
        drop=$(echo "$WK" | jq -r '.dropped // 0')
        queue=$(echo "$WK" | jq -r '.queue_size // 0')
        workers=$(echo "$WK" | jq -r '.workers // 0')
        active=$(echo "$WK" | jq -r '.active_workers // 0')
        w_conns=$(echo "$WK" | jq -r '.connections // 0')
        tp_in=$(echo "$WK" | jq -r '.throughput_in // 0')
        tp_out=$(echo "$WK" | jq -r '.throughput_out // 0')
        w_p50=$(echo "$WK" | jq -r '.latency_p50_us // 0')
        w_p99=$(echo "$WK" | jq -r '.latency_p99_us // 0')

        echo -e "${CYAN}║${NC} ${GREEN}▶ WORKER-V2${NC}                                                             ${CYAN}║${NC}"
        printf "${CYAN}║${NC}   Workers: ${WHITE}%-5s${NC} (active: ${WHITE}%-5s${NC})  Queue: ${WHITE}%-10s${NC} Conns: ${WHITE}%-5s${NC}  ${CYAN}║${NC}\n" \
            "$workers" "$active" "$(format_number $queue)" "$w_conns"
        printf "${CYAN}║${NC}   Received: ${WHITE}%-12s${NC}  Processed: ${WHITE}%-12s${NC}  Dropped: ${WHITE}%-8s${NC}${CYAN}║${NC}\n" \
            "$(format_number $recv_w)" "$(format_number $proc)" "$(format_number $drop)"
        printf "${CYAN}║${NC}   Throughput: IN ${WHITE}%-10s${NC}/s  OUT ${WHITE}%-10s${NC}/s                   ${CYAN}║${NC}\n" \
            "$(format_number $tp_in)" "$(format_number $tp_out)"
        printf "${CYAN}║${NC}   Processing Latency: P50 ${WHITE}%-8s${NC} μs  P99 ${WHITE}%-8s${NC} μs            ${CYAN}║${NC}\n" \
            "$(format_number $w_p50)" "$(format_number $w_p99)"
    else
        echo -e "${CYAN}║${NC} ${RED}▶ WORKER-V2: Not connected${NC}                                             ${CYAN}║${NC}"
    fi

    echo -e "${CYAN}╠══════════════════════════════════════════════════════════════════════════╣${NC}"

    # EMQX Section
    if [ -n "$EMQX" ] && [ "$EMQX" != "{}" ]; then
        e_conns=$(echo "$EMQX" | jq -r '.["connections.count"] // 0')
        e_topics=$(echo "$EMQX" | jq -r '.["topics.count"] // 0')
        e_subs=$(echo "$EMQX" | jq -r '.["subscriptions.count"] // 0')
        e_shared=$(echo "$EMQX" | jq -r '.["subscriptions.shared.count"] // 0')
        e_msg_in=$(echo "$EMQX" | jq -r '.["messages.received"] // 0')
        e_msg_out=$(echo "$EMQX" | jq -r '.["messages.sent"] // 0')

        echo -e "${CYAN}║${NC} ${GREEN}▶ EMQX BROKER${NC}                                                           ${CYAN}║${NC}"
        printf "${CYAN}║${NC}   Connections: ${WHITE}%-10s${NC}  Topics: ${WHITE}%-8s${NC}  Subscriptions: ${WHITE}%-8s${NC} ${CYAN}║${NC}\n" \
            "$(format_number $e_conns)" "$e_topics" "$(format_number $e_subs)"
        printf "${CYAN}║${NC}   Shared Subs: ${WHITE}%-10s${NC}  Messages In: ${WHITE}%-12s${NC}               ${CYAN}║${NC}\n" \
            "$e_shared" "$(format_number $e_msg_in)"
    else
        echo -e "${CYAN}║${NC} ${RED}▶ EMQX: Not connected${NC}                                                  ${CYAN}║${NC}"
    fi

    echo -e "${CYAN}╠══════════════════════════════════════════════════════════════════════════╣${NC}"

    # Docker Stats
    echo -e "${CYAN}║${NC} ${GREEN}▶ CONTAINER RESOURCES${NC}                                                    ${CYAN}║${NC}"
    docker stats --no-stream --format "{{.Name}}: CPU {{.CPUPerc}} | Mem {{.MemUsage}}" 2>/dev/null | head -5 | while read line; do
        printf "${CYAN}║${NC}   %-70s ${CYAN}║${NC}\n" "$line"
    done

    echo -e "${CYAN}╠══════════════════════════════════════════════════════════════════════════╣${NC}"

    # Efficiency metrics
    if [ -n "$LG" ] && [ "$LG" != "{}" ]; then
        ratio=$((clients / (conns > 0 ? conns : 1)))
        echo -e "${CYAN}║${NC} ${YELLOW}▶ EFFICIENCY METRICS${NC}                                                     ${CYAN}║${NC}"
        printf "${CYAN}║${NC}   Multiplexing Ratio: ${WHITE}1 connection : %s virtual clients${NC}           ${CYAN}║${NC}\n" \
            "$(format_number $ratio)"
    fi

    echo -e "${CYAN}╚══════════════════════════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "Press ${WHITE}Ctrl+C${NC} to exit | Refresh: ${REFRESH_INTERVAL}s"

    sleep $REFRESH_INTERVAL
done
