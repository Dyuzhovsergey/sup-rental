package user

import (
	"context"
	"errors"
	"testing"
)

func TestOperatorServiceCreate(t *testing.T) {
	repository := &operatorRepositoryStub{}
	hasher := &operatorPasswordHasherStub{hash: "argon2id-hash"}
	service := NewOperatorService(repository, hasher)
	actor := adminFixture()

	created, err := service.Create(context.Background(), actor, "  NEW.Operator  ", "secret1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Login != "new.operator" || created.Role != RoleOperator || !created.Active {
		t.Errorf("Create() = %+v", created)
	}
	if repository.createdHash != "argon2id-hash" || repository.createdActor != actor {
		t.Errorf("repository arguments = hash %q, actor %+v", repository.createdHash, repository.createdActor)
	}
}

func TestOperatorServiceRequiresActiveAdmin(t *testing.T) {
	service := NewOperatorService(&operatorRepositoryStub{}, &operatorPasswordHasherStub{hash: "hash"})
	actors := []User{
		{},
		{ID: 2, Role: RoleOperator, Active: true},
		{ID: 1, Role: RoleAdmin, Active: false},
	}
	for _, actor := range actors {
		_, err := service.List(context.Background(), actor)
		if !errors.Is(err, ErrAccessDenied) {
			t.Errorf("List(%+v) error = %v, want ErrAccessDenied", actor, err)
		}
	}
}

func TestOperatorServiceValidatesBeforeRepository(t *testing.T) {
	repository := &operatorRepositoryStub{}
	service := NewOperatorService(repository, &operatorPasswordHasherStub{err: errors.New("hash failed")})
	_, err := service.Create(context.Background(), adminFixture(), "bad login!", "secret1")
	if !errors.Is(err, ErrInvalidLogin) || repository.createCalls != 0 {
		t.Fatalf("Create() error = %v, create calls = %d", err, repository.createCalls)
	}
}

func TestOperatorServiceChangesStateAndPassword(t *testing.T) {
	repository := &operatorRepositoryStub{}
	service := NewOperatorService(repository, &operatorPasswordHasherStub{hash: "new-hash"})
	actor := adminFixture()

	if _, err := service.Disable(context.Background(), actor, 17); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if repository.activeID != 17 || repository.activeValue {
		t.Errorf("Disable() repository values = %d, %t", repository.activeID, repository.activeValue)
	}
	if _, err := service.Activate(context.Background(), actor, 17); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if !repository.activeValue {
		t.Error("Activate() did not request active state")
	}
	if _, err := service.ChangePassword(context.Background(), actor, 17, "newpass"); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}
	if repository.passwordID != 17 || repository.passwordHash != "new-hash" {
		t.Errorf("ChangePassword() repository values = %d, %q", repository.passwordID, repository.passwordHash)
	}
}

type operatorPasswordHasherStub struct {
	hash string
	err  error
}

func (s *operatorPasswordHasherStub) Hash(string) (string, error) { return s.hash, s.err }

type operatorRepositoryStub struct {
	createCalls  int
	createdActor User
	createdHash  string
	activeID     int64
	activeValue  bool
	passwordID   int64
	passwordHash string
}

func (s *operatorRepositoryStub) ListOperators(context.Context) ([]User, error) { return nil, nil }
func (s *operatorRepositoryStub) GetOperator(context.Context, int64) (User, error) {
	return User{ID: 17, Login: "operator", Role: RoleOperator, Active: true}, nil
}
func (s *operatorRepositoryStub) CreateOperator(_ context.Context, actor User, account User, hash string) (User, error) {
	s.createCalls++
	s.createdActor, s.createdHash = actor, hash
	account.ID = 17
	return account, nil
}
func (s *operatorRepositoryStub) SetOperatorActive(_ context.Context, _ User, id int64, active bool) (User, error) {
	s.activeID, s.activeValue = id, active
	return User{ID: id, Role: RoleOperator, Active: active}, nil
}
func (s *operatorRepositoryStub) ChangeOperatorPassword(_ context.Context, _ User, id int64, hash string) (User, error) {
	s.passwordID, s.passwordHash = id, hash
	return User{ID: id, Role: RoleOperator, Active: true}, nil
}

func adminFixture() User {
	return User{ID: 1, Login: "admin", Role: RoleAdmin, Active: true}
}
