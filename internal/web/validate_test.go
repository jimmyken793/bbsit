package web

import (
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
