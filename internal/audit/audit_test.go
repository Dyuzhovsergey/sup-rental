package audit

import (
	"context"
	"errors"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestNewFilterValidatesAllowlistedValuesAndPage(t *testing.T) {
	tests := []struct {
		name, category, result string
		page                   int
		wantErr                error
	}{
		{name: "valid", category: " equipment ", result: ResultSuccess, page: 2},
		{name: "valid clients", category: " clients ", result: ResultSuccess, page: 1},
		{name: "valid rentals", category: " rentals ", result: ResultSuccess, page: 1},
		{name: "unknown category", category: "secret", page: 1, wantErr: ErrInvalidFilter},
		{name: "unknown result", result: "unknown", page: 1, wantErr: ErrInvalidFilter},
		{name: "invalid page", page: 0, wantErr: ErrInvalidPage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter, err := NewFilter(tt.category, tt.result, " admin ", " SUP-001 ", nil, nil, tt.page)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewFilter() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (filter.Actor != "admin" || filter.Target != "SUP-001") {
				t.Errorf("NewFilter() = %+v", filter)
			}
		})
	}
}

func TestServiceListRequiresActiveAdmin(t *testing.T) {
	repositoryCalls := 0
	repository := &repositoryStub{list: func(context.Context, Filter) (Page, error) {
		repositoryCalls++
		return Page{}, nil
	}}
	service := NewService(repository)

	_, err := service.List(context.Background(), user.User{ID: 2, Role: user.RoleOperator, Active: true}, Filter{Page: 1})
	if !errors.Is(err, user.ErrAccessDenied) || repositoryCalls != 0 {
		t.Fatalf("List() error = %v, repository calls = %d", err, repositoryCalls)
	}
}

type repositoryStub struct {
	list func(context.Context, Filter) (Page, error)
}

func (r *repositoryStub) List(ctx context.Context, filter Filter) (Page, error) {
	return r.list(ctx, filter)
}
