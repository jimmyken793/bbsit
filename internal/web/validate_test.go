package web

import (
	"strings"
	"testing"

	"github.com/kingyoung/bbsit/internal/types"
)

func TestValidateAndDefaultProject_NormalisesVolumes(t *testing.T) {
	s := &Server{stackRoot: "/mnt/nvme/bbsit/stacks"}
	p := &types.Project{
		ID:        "company-site-scraper",
		StackPath: "/mnt/nvme/bbsit/stacks/company-site-scraper",
		Services: []types.ServiceConfig{{
			Name:          "app",
			RegistryImage: "x",
			Volumes: []types.VolumeMount{
				{HostPath: "/mnt/nvme/bbsit/stacks/company-site-scraper/data", ContainerPath: "/data"},
				{HostPath: "./logs", ContainerPath: "/logs"},
				{HostPath: "/var/log/app", ContainerPath: "/varlog"},
			},
		}},
	}
	if err := s.validateAndDefaultProject(p, false); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := p.Services[0].Volumes
	if got[0].HostPath != "data" {
		t.Errorf("absolute-under-stack: got %q, want %q", got[0].HostPath, "data")
	}
	if got[1].HostPath != "logs" {
		t.Errorf("./logs: got %q, want %q", got[1].HostPath, "logs")
	}
	if got[2].HostPath != "/var/log/app" {
		t.Errorf("outside-stack: got %q, want %q", got[2].HostPath, "/var/log/app")
	}
}

func TestValidateAndDefaultProject_InjectsBackupVolume(t *testing.T) {
	s := &Server{stackRoot: "/mnt/nvme/bbsit/stacks"}
	p := &types.Project{
		ID: "gitlab",
		Services: []types.ServiceConfig{{
			Name:          "gitlab",
			RegistryImage: "gitlab/gitlab-ce",
			Volumes: []types.VolumeMount{
				{HostPath: "data", ContainerPath: "/var/opt/gitlab"},
			},
		}},
		Backup: &types.BackupSpec{
			Service:        "gitlab",
			BackupCommand:  "gitlab-backup create",
			RestoreCommand: "gitlab-backup restore",
			OutputPath:     "/var/opt/gitlab/backups",
			OutputPattern:  "*_gitlab_backup.tar",
		},
	}
	if err := s.validateAndDefaultProject(p, true); err != nil {
		t.Fatalf("validate: %v", err)
	}
	vols := p.Services[0].Volumes
	if len(vols) != 2 {
		t.Fatalf("expected 2 volumes, got %d: %+v", len(vols), vols)
	}
	got := vols[1]
	if got.HostPath != "backups" || got.ContainerPath != "/var/opt/gitlab/backups" {
		t.Errorf("backup mount = %+v, want host=backups container=/var/opt/gitlab/backups", got)
	}
}

func TestValidateAndDefaultProject_BackupVolumeAlreadyConfigured(t *testing.T) {
	// If the operator already mounted the output_path themselves, we leave
	// their config alone instead of duplicating the bind.
	s := &Server{stackRoot: "/x"}
	p := &types.Project{
		ID: "gitlab",
		Services: []types.ServiceConfig{{
			Name:          "gitlab",
			RegistryImage: "gitlab/gitlab-ce",
			Volumes: []types.VolumeMount{
				{HostPath: "my-backups", ContainerPath: "/var/opt/gitlab/backups"},
			},
		}},
		Backup: &types.BackupSpec{
			Service:       "gitlab",
			BackupCommand: "gitlab-backup create",
			OutputPath:    "/var/opt/gitlab/backups",
		},
	}
	if err := s.validateAndDefaultProject(p, true); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(p.Services[0].Volumes) != 1 {
		t.Errorf("expected pre-existing mount preserved, got %+v", p.Services[0].Volumes)
	}
	if p.Services[0].Volumes[0].HostPath != "my-backups" {
		t.Errorf("operator's mount overwritten: %+v", p.Services[0].Volumes[0])
	}
}

func TestValidateAndDefaultProject_BackupValidationErrors(t *testing.T) {
	s := &Server{stackRoot: "/x"}
	cases := []struct {
		name   string
		spec   *types.BackupSpec
		errSub string
	}{
		{"missing service", &types.BackupSpec{BackupCommand: "x", OutputPath: "/o"}, "backup.service required"},
		{"missing command", &types.BackupSpec{Service: "s", OutputPath: "/o"}, "backup_command required"},
		{"missing output_path", &types.BackupSpec{Service: "s", BackupCommand: "x"}, "output_path required"},
		{"relative output_path", &types.BackupSpec{Service: "s", BackupCommand: "x", OutputPath: "relative"}, "must be absolute"},
		{"unknown service", &types.BackupSpec{Service: "ghost", BackupCommand: "x", OutputPath: "/o"}, "not found among services"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &types.Project{
				ID: "p",
				Services: []types.ServiceConfig{{Name: "s", RegistryImage: "i"}},
				Backup:   tc.spec,
			}
			err := s.validateAndDefaultProject(p, true)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.errSub)
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("err = %v, want substring %q", err, tc.errSub)
			}
		})
	}
}
