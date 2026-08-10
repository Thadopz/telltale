package tools

import "github.com/Thadopz/telltale/internal/authz"

type RiskLevel = authz.RiskLevel

const (
	RiskReadOnly RiskLevel = authz.RiskReadOnly
	RiskWrite    RiskLevel = authz.RiskWrite
)

type ExecutionPolicy struct {
	ReadOnly       bool
	Cancellable    bool
	Idempotent     bool
	OperationAware bool
}

type Metadata struct {
	Name               string
	AgentVisible       bool
	Risk               RiskLevel
	Permission         authz.Permission
	RequiresConfirm    bool
	AllowedAgentScopes []authz.AgentScope
	RequiredCapability string
	ExecutionPolicy    ExecutionPolicy
	Description        string
}

var metadataByName = map[string]Metadata{
	"query_internal_docs": {
		Name:         "query_internal_docs",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermDocsRead,
		Description:  "Search internal documents and knowledge base.",
	},
	"get_current_time": {
		Name:         "get_current_time",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermTimeRead,
		Description:  "Return the current time.",
	},
	"query_prometheus_alerts": {
		Name:         "query_prometheus_alerts",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermMetricsRead,
		Description:  "Query alert and metrics data.",
	},
	kubectlToolName: {
		Name:         kubectlToolName,
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermSREGym,
		Description:  "Execute kubectl commands in SREGym.",
	},
	"mysql_crud": {
		Name:            "mysql_crud",
		AgentVisible:    false,
		Risk:            RiskWrite,
		Permission:      authz.PermDatabaseWrite,
		RequiresConfirm: true,
		ExecutionPolicy: ExecutionPolicy{ReadOnly: false, Cancellable: true, Idempotent: false, OperationAware: true},
		Description:     "Run MySQL statements. Write operations are disabled unless explicitly enabled by server config.",
	},
	"db_slow_queries": {
		Name:               "db_slow_queries",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermDatabaseRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeDatabase},
		Description:        "Find bounded slow-query records for the configured business database.",
	},
	"db_server_health": {
		Name:               "db_server_health",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermDatabaseRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeDatabase},
		Description:        "Inspect fixed health counters for the configured business database.",
	},
	"db_active_queries": {
		Name:               "db_active_queries",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermDatabaseRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeDatabase},
		Description:        "Inspect bounded active queries for the configured business database.",
	},
	"db_wait_events": {
		Name:               "db_wait_events",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermDatabaseRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeDatabase},
		Description:        "Inspect aggregated wait events for the configured business database.",
	},
	"db_locks": {
		Name:               "db_locks",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermDatabaseRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopeDatabase},
		Description:        "Inspect current lock waits for the configured business database.",
	},
	// These names are compatibility aliases for direct legacy integrations.
	// They are deliberately hidden from all Agent tool lists.
	"mysql_slow_sql": {
		Name:         "mysql_slow_sql",
		AgentVisible: false,
		Risk:         RiskReadOnly,
		Permission:   authz.PermDatabaseRead,
		Description:  "Deprecated compatibility alias for db_slow_queries.",
	},
	"mysql_server_health": {
		Name:         "mysql_server_health",
		AgentVisible: false,
		Risk:         RiskReadOnly,
		Permission:   authz.PermDatabaseRead,
		Description:  "Deprecated compatibility alias for db_server_health.",
	},
	"mysql_active_queries": {
		Name:         "mysql_active_queries",
		AgentVisible: false,
		Risk:         RiskReadOnly,
		Permission:   authz.PermDatabaseRead,
		Description:  "Deprecated compatibility alias for db_active_queries.",
	},
	"mysql_wait_events": {
		Name:         "mysql_wait_events",
		AgentVisible: false,
		Risk:         RiskReadOnly,
		Permission:   authz.PermDatabaseRead,
		Description:  "Deprecated compatibility alias for db_wait_events.",
	},
	"mysql_locks": {
		Name:         "mysql_locks",
		AgentVisible: false,
		Risk:         RiskReadOnly,
		Permission:   authz.PermDatabaseRead,
		Description:  "Deprecated compatibility alias for db_locks.",
	},
	"query_payment_anomaly": {
		Name:         "query_payment_anomaly",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermPaymentRead,
		Description:  "Query payment anomaly events from the local payment observability agent.",
	},
	"query_payment_metrics": {
		Name:         "query_payment_metrics",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermPaymentRead,
		Description:  "Query payment business metrics and success rates.",
	},
	"query_payment_order_detail": {
		Name:         "query_payment_order_detail",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermPaymentRead,
		Description:  "Query payment order detail, transaction, refunds, and notify tasks.",
	},
	"check_payment_consistency": {
		Name:         "check_payment_consistency",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermPaymentRead,
		Description:  "Check payment order and transaction consistency.",
	},
	"search_payment_logs": {
		Name:         "search_payment_logs",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermPaymentRead,
		Description:  "Search payment diagnostic logs.",
	},
	"query_payment_status_distribution": {
		Name:         "query_payment_status_distribution",
		AgentVisible: true,
		Risk:         RiskReadOnly,
		Permission:   authz.PermPaymentRead,
		Description:  "Query recent payment order status distribution.",
	},
	"code_search": {
		Name:               "code_search",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermCodeRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopePayment},
		RequiredCapability: "code.read",
		Description:        "Search the fixed, sanitized card_service source snapshot for precise implementation anchors.",
	},
	"code_read": {
		Name:               "code_read",
		AgentVisible:       true,
		Risk:               RiskReadOnly,
		Permission:         authz.PermCodeRead,
		AllowedAgentScopes: []authz.AgentScope{authz.AgentScopePayment},
		RequiredCapability: "code.read",
		Description:        "Read a bounded code snippet using a handle returned by code_search.",
	},
}

func ExecutionPolicyFor(name string) ExecutionPolicy {
	meta, ok := LookupMetadata(name)
	if ok && (meta.ExecutionPolicy != (ExecutionPolicy{})) {
		return meta.ExecutionPolicy
	}
	if ok && meta.Risk == RiskWrite {
		return ExecutionPolicy{ReadOnly: false, Cancellable: false, Idempotent: false, OperationAware: false}
	}
	return ExecutionPolicy{ReadOnly: true, Cancellable: true, Idempotent: true}
}

func LookupMetadata(name string) (Metadata, bool) {
	meta, ok := metadataByName[name]
	return meta, ok
}

func IsWriteOperation(operateType string) bool {
	switch operateType {
	case "insert", "update", "delete":
		return true
	default:
		return false
	}
}
