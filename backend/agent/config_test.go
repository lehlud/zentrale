package agent

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestConfigurationMutationsPublishOnlySuccessfulChanges(t *testing.T) {
	for _, test := range []struct {
		name       string
		usesSSHKey bool
		mutate     func(*Engine, domain.Project, int, bool) error
	}{
		{
			name:       "machine",
			usesSSHKey: true,
			mutate: func(engine *Engine, _ domain.Project, keyID int, _ bool) error {
				_, err := engine.SaveMachine(domain.Machine{
					ID: "new-worker", Name: "New worker", Host: "worker.example.test",
					User: "developer", KeyID: keyID, Sandbox: "none",
				})
				return err
			},
		},
		{
			name:       "source",
			usesSSHKey: true,
			mutate: func(engine *Engine, project domain.Project, keyID int, _ bool) error {
				_, err := engine.SaveSource(domain.Source{
					ID: "new-source", Name: "repository", ProjectID: project.ID,
					URL: "git@github.com:owner/repository.git", KeyID: keyID,
				})
				return err
			},
		},
		{
			name: "project",
			mutate: func(engine *Engine, project domain.Project, _ int, fail bool) error {
				project.Name = "Updated project"
				if fail {
					project.GitConnectionID, project.RepositoryID = "missing-account", "owner/repository"
				}
				_, err := engine.SaveProject(project)
				return err
			},
		},
	} {
		for _, fail := range []bool{false, true} {
			outcome := "success"
			if fail {
				outcome = "failure"
			}
			t.Run(test.name+"/"+outcome, func(t *testing.T) {
				engine, record := testEngine(t, nil)
				var project domain.Project
				if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
					t.Fatal(err)
				}

				var keyID int
				if test.usesSSHKey {
					key, err := engine.Store.GenerateSSHKey()
					if err != nil {
						t.Fatal(err)
					}
					keyID = key.ID
					if fail {
						// Validation accepts this existing key, but the save transaction
						// must reject using another machine's credential.
						owner := domain.Machine{ID: "key-owner", KeyID: keyID}
						if err := engine.Store.SaveWithSSHKeys("machine", owner.ID, "", owner,
							datastore.SSHKeyClaim{KeyID: keyID, Purpose: "machine", PurposeID: owner.ID}); err != nil {
							t.Fatal(err)
						}
					}
				}

				events, unsubscribe := engine.Subscribe()
				defer unsubscribe()
				<-events // Initial response_reset is unrelated to the mutation.

				if err := test.mutate(engine, project, keyID, fail); (err != nil) != fail {
					t.Fatalf("mutation error = %v, want failure %v", err, fail)
				}

				if !fail {
					select {
					case event := <-events:
						if event.Type != "resources" {
							t.Fatalf("unexpected event: %#v", event)
						}
					default:
						t.Fatal("successful mutation did not publish its resource change")
					}
				}
				select {
				case event := <-events:
					t.Fatalf("failed mutation or duplicate publication produced an event: %#v", event)
				default:
				}
			})
		}
	}
}

func TestConfigurationMutationsPreserveUnreadableExistingRecords(t *testing.T) {
	for _, kind := range []string{"project", "source"} {
		t.Run(kind, func(t *testing.T) {
			engine, record := testEngine(t, nil)
			var project domain.Project
			if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
				t.Fatal(err)
			}
			id := project.ID
			stored := map[string]any{"id": id, "machineIds": "unreadable", "managedWorkspace": true}
			var saveErr error
			if kind == "source" {
				id = "unreadable-source"
				stored = map[string]any{
					"id": id, "projectId": project.ID, "bindingId": "managed-binding", "name": true,
				}
			}
			if err := engine.Store.Save(kind, id, project.ID, stored); err != nil {
				t.Fatal(err)
			}

			if kind == "project" {
				_, saveErr = engine.SaveProject(project)
			} else {
				key, err := engine.Store.GenerateSSHKey()
				if err != nil {
					t.Fatal(err)
				}
				_, saveErr = engine.SaveSource(domain.Source{
					ID: id, Name: "repository", ProjectID: project.ID,
					URL: "git@github.com:owner/repository.git", KeyID: key.ID,
				})
			}
			var decodeErr *json.UnmarshalTypeError
			if !errors.As(saveErr, &decodeErr) {
				t.Fatalf("existing record's decode error was ignored: %v", saveErr)
			}

			var got map[string]any
			if err := engine.Store.Load(kind, id, &got); err != nil || !reflect.DeepEqual(got, stored) {
				t.Fatalf("unreadable record was overwritten: %#v, %v", got, err)
			}
		})
	}
}
