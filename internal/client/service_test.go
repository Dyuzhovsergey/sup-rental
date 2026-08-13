package client

import (
	"context"
	"errors"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestServiceCreateNormalizesAndPassesOperator(t *testing.T) {
	actor := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}
	repository := &clientRepositoryStub{create: func(_ context.Context, gotActor user.User, customer Client) (Client, error) {
		if gotActor != actor {
			t.Errorf("actor = %+v, want %+v", gotActor, actor)
		}
		customer.ID = 19
		return customer, nil
	}}
	created, err := NewService(repository).Create(context.Background(), actor, "  Анна   Иванова ", "8 (999) 123-45-67")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID != 19 || created.FullName != "Анна Иванова" || created.Phone != "+79991234567" {
		t.Errorf("Create() = %+v", created)
	}
}

func TestServiceCreateRequiresActiveOperator(t *testing.T) {
	tests := []user.User{
		{},
		{ID: 1, Role: user.RoleAdmin, Active: true},
		{ID: 2, Role: user.RoleOperator, Active: false},
	}
	for _, actor := range tests {
		_, err := NewService(&clientRepositoryStub{}).Create(context.Background(), actor, "Анна Иванова", "+79991234567")
		if !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("Create(%+v) error = %v, want ErrAccessDenied", actor, err)
		}
	}
}

func TestServiceFindByPhoneNormalizesInput(t *testing.T) {
	repository := &clientRepositoryStub{find: func(_ context.Context, phone Phone) (Client, error) {
		if phone != "+79991234567" {
			t.Errorf("phone = %q", phone)
		}
		return Client{ID: 1, FullName: "Анна Иванова", Phone: phone}, nil
	}}
	got, err := NewService(repository).FindByPhone(context.Background(), "8 (999) 123-45-67")
	if err != nil || got.ID != 1 {
		t.Fatalf("FindByPhone() = %+v, %v", got, err)
	}
}

func TestServiceValidatesCreateSearchAndPage(t *testing.T) {
	service := NewService(&clientRepositoryStub{})
	actor := user.User{ID: 7, Role: user.RoleOperator, Active: true}
	if _, err := service.Create(context.Background(), actor, "", "+79991234567"); !errors.Is(err, ErrFullNameRequired) {
		t.Errorf("empty name error = %v", err)
	}
	if _, err := service.Create(context.Background(), actor, "Анна", "bad"); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("invalid create phone error = %v", err)
	}
	if _, err := service.FindByPhone(context.Background(), "bad"); !errors.Is(err, ErrInvalidPhone) {
		t.Errorf("invalid search phone error = %v", err)
	}
	if _, err := service.ListPage(context.Background(), 0); !errors.Is(err, ErrInvalidPage) {
		t.Errorf("invalid page error = %v", err)
	}
}

type clientRepositoryStub struct {
	create func(context.Context, user.User, Client) (Client, error)
	get    func(context.Context, int64) (Client, error)
	find   func(context.Context, Phone) (Client, error)
	list   func(context.Context, int, int) (Page, error)
}

func (s *clientRepositoryStub) Create(ctx context.Context, actor user.User, customer Client) (Client, error) {
	if s.create == nil {
		return Client{}, nil
	}
	return s.create(ctx, actor, customer)
}
func (s *clientRepositoryStub) Get(ctx context.Context, id int64) (Client, error) {
	if s.get == nil {
		return Client{}, ErrClientNotFound
	}
	return s.get(ctx, id)
}
func (s *clientRepositoryStub) FindByPhone(ctx context.Context, phone Phone) (Client, error) {
	if s.find == nil {
		return Client{}, ErrClientNotFound
	}
	return s.find(ctx, phone)
}
func (s *clientRepositoryStub) ListPage(ctx context.Context, page, size int) (Page, error) {
	if s.list == nil {
		return Page{Page: page}, nil
	}
	return s.list(ctx, page, size)
}
