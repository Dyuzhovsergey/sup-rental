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

func TestServiceUpdateNormalizesAndPassesOperator(t *testing.T) {
	actor := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}
	repository := &clientRepositoryStub{update: func(_ context.Context, gotActor user.User, customer Client) (Client, error) {
		if gotActor != actor {
			t.Errorf("actor = %+v, want %+v", gotActor, actor)
		}
		return customer, nil
	}}

	updated, err := NewService(repository).Update(
		context.Background(), actor, 19, "  Анна   Петрова ", "8 (999) 765-43-21",
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ID != 19 || updated.FullName != "Анна Петрова" || updated.Phone != "+79997654321" {
		t.Errorf("Update() = %+v", updated)
	}
}

func TestServiceUpdateRequiresActiveOperator(t *testing.T) {
	tests := []user.User{
		{},
		{ID: 1, Role: user.RoleAdmin, Active: true},
		{ID: 2, Role: user.RoleOperator, Active: false},
	}
	for _, actor := range tests {
		_, err := NewService(&clientRepositoryStub{}).Update(
			context.Background(), actor, 19, "Анна Иванова", "+79991234567",
		)
		if !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("Update(%+v) error = %v, want ErrAccessDenied", actor, err)
		}
	}
}

func TestServiceUpdateValidatesIDAndContactData(t *testing.T) {
	service := NewService(&clientRepositoryStub{})
	actor := user.User{ID: 7, Role: user.RoleOperator, Active: true}
	tests := []struct {
		name, fullName, phone string
		id                    int64
		want                  error
	}{
		{name: "ID", id: 0, fullName: "Анна", phone: "+79991234567", want: ErrClientNotFound},
		{name: "name", id: 1, fullName: "", phone: "+79991234567", want: ErrFullNameRequired},
		{name: "phone", id: 1, fullName: "Анна", phone: "bad", want: ErrInvalidPhone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Update(context.Background(), actor, tt.id, tt.fullName, tt.phone)
			if !errors.Is(err, tt.want) {
				t.Errorf("Update() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServiceUpdateWrapsRepositoryError(t *testing.T) {
	repositoryError := errors.New("repository unavailable")
	repository := &clientRepositoryStub{update: func(context.Context, user.User, Client) (Client, error) {
		return Client{}, repositoryError
	}}
	actor := user.User{ID: 7, Role: user.RoleOperator, Active: true}

	_, err := NewService(repository).Update(
		context.Background(), actor, 19, "Анна Иванова", "+79991234567",
	)
	if !errors.Is(err, repositoryError) || err.Error() != "update client: repository unavailable" {
		t.Errorf("Update() error = %v", err)
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
	if _, err := service.ListPage(context.Background(), 0, DefaultPageSize); !errors.Is(err, ErrInvalidPage) {
		t.Errorf("invalid page error = %v", err)
	}
	if _, err := service.ListPage(context.Background(), 1, 7); !errors.Is(err, ErrInvalidPage) {
		t.Errorf("invalid page size error = %v", err)
	}
}

func TestServiceListPagePassesAllowedPageSize(t *testing.T) {
	repository := &clientRepositoryStub{list: func(_ context.Context, page, size int) (Page, error) {
		if page != 2 || size != 15 {
			t.Errorf("ListPage() page=%d size=%d", page, size)
		}
		return Page{Page: page, Total: 18}, nil
	}}
	result, err := NewService(repository).ListPage(context.Background(), 2, 15)
	if err != nil || result.Page != 2 || result.Total != 18 {
		t.Errorf("ListPage() = %+v, %v", result, err)
	}
}

func TestAllowedPageSizesReturnsCopy(t *testing.T) {
	first := AllowedPageSizes()
	first[0] = 100
	second := AllowedPageSizes()
	if second[0] != 5 {
		t.Errorf("AllowedPageSizes() shared mutable data: %v", second)
	}
}

type clientRepositoryStub struct {
	create func(context.Context, user.User, Client) (Client, error)
	update func(context.Context, user.User, Client) (Client, error)
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
func (s *clientRepositoryStub) Update(ctx context.Context, actor user.User, customer Client) (Client, error) {
	if s.update == nil {
		return Client{}, nil
	}
	return s.update(ctx, actor, customer)
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
