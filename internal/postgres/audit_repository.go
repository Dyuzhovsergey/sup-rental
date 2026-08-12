package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/audit"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditRepository читает постоянный журнал без изменения его записей.
type AuditRepository struct{ pool *pgxpool.Pool }

// NewAuditRepository создаёт PostgreSQL repository журнала аудита.
func NewAuditRepository(pool *pgxpool.Pool) *AuditRepository { return &AuditRepository{pool: pool} }

// List возвращает события по проверенным фильтрам от новых к старым.
func (r *AuditRepository) List(ctx context.Context, filter audit.Filter) (audit.Page, error) {
	where, arguments := auditWhere(filter)
	var total int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM audit_events"+where, arguments...).Scan(&total); err != nil {
		return audit.Page{}, fmt.Errorf("count audit events: %w", err)
	}

	arguments = append(arguments, audit.PageSize, (filter.Page-1)*audit.PageSize)
	query := `
		SELECT id, occurred_at, actor_user_id, actor_login, actor_role,
		       action, target_type, target_id, target_label, result, details
		FROM audit_events` + where + fmt.Sprintf(`
		ORDER BY occurred_at DESC, id DESC
		LIMIT $%d OFFSET $%d`, len(arguments)-1, len(arguments))
	rows, err := r.pool.Query(ctx, query, arguments...)
	if err != nil {
		return audit.Page{}, fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	events := make([]audit.Event, 0, audit.PageSize)
	for rows.Next() {
		var event audit.Event
		if err := rows.Scan(
			&event.ID, &event.OccurredAt, &event.ActorUserID, &event.ActorLogin,
			&event.ActorRole, &event.Action, &event.TargetType, &event.TargetID,
			&event.TargetLabel, &event.Result, &event.Details,
		); err != nil {
			return audit.Page{}, fmt.Errorf("scan audit event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return audit.Page{}, fmt.Errorf("iterate audit events: %w", err)
	}
	return audit.Page{Events: events, Total: total, Page: filter.Page}, nil
}

func auditWhere(filter audit.Filter) (string, []any) {
	clauses := make([]string, 0, 6)
	arguments := make([]any, 0, 6)
	add := func(clause string, value any) {
		arguments = append(arguments, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(arguments)))
	}
	switch filter.Category {
	case audit.CategoryAuth:
		add("action LIKE $%d", "auth.%")
	case audit.CategoryUsers:
		add("(action LIKE $%d OR action LIKE 'operator.%%')", "admin.%")
	case audit.CategoryEquipment:
		add("action LIKE $%d", "equipment.%")
	}
	if filter.Result != "" {
		add("result = $%d", filter.Result)
	}
	if filter.Actor != "" {
		add("actor_login ILIKE '%%' || $%d || '%%'", filter.Actor)
	}
	if filter.Target != "" {
		add("target_label ILIKE '%%' || $%d || '%%'", filter.Target)
	}
	if filter.From != nil {
		add("occurred_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("occurred_at < $%d", *filter.To)
	}
	if len(clauses) == 0 {
		return "", arguments
	}
	return " WHERE " + strings.Join(clauses, " AND "), arguments
}
