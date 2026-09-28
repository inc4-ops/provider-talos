package tfwrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/siderolabs/talos/pkg/machinery/config"
	"github.com/siderolabs/talos/pkg/machinery/config/generate"
	"github.com/siderolabs/talos/pkg/machinery/config/generate/secrets"
	"github.com/siderolabs/talos/pkg/machinery/config/machine"
	"github.com/siderolabs/terraform-provider-talos/pkg/talos"
)

const typeName = "talos_machine_configuration_apply"

func workerConfig(t *testing.T) string {
	t.Helper()
	sb, err := secrets.NewBundle(secrets.NewClock(), config.TalosVersionCurrent)
	if err != nil {
		t.Fatal(err)
	}
	in, err := generate.NewInput("c1", "https://10.0.0.1:6443", "1.36.0", generate.WithSecretsBundle(sb))
	if err != nil {
		t.Fatal(err)
	}
	p, err := in.Config(machine.TypeWorker)
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type harness struct {
	srv tfprotov6.ProviderServer
	ty  tftypes.Type
}

func newHarness(t *testing.T, p provider.Provider) harness {
	ctx := context.Background()
	srv := providerserver.NewProtocol6(p)()
	ps, _ := srv.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	pty := ps.Provider.ValueType()
	pv, _ := tfprotov6.NewDynamicValue(pty, tftypes.NewValue(pty, map[string]tftypes.Value{"image_factory_url": tftypes.NewValue(tftypes.String, nil)}))
	if _, err := srv.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: &pv}); err != nil {
		t.Fatal(err)
	}
	sr := &fwresource.SchemaResponse{}
	talos.NewTalosMachineConfigurationApplyResource().Schema(ctx, fwresource.SchemaRequest{}, sr)
	return harness{srv: srv, ty: sr.Schema.Type().TerraformType(ctx)}
}

func (h harness) val(t *testing.T, m map[string]any) tftypes.Value {
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	v, err := tftypes.ValueFromJSONWithOpts(b, h.ty, tftypes.ValueFromJSONOpts{IgnoreUndefinedAttributes: true})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (h harness) plan(t *testing.T, prior, cfg tftypes.Value) map[string]tftypes.Value {
	ctx := context.Background()
	pdv, _ := tfprotov6.NewDynamicValue(h.ty, prior)
	cdv, _ := tfprotov6.NewDynamicValue(h.ty, cfg)
	proposed := cdv
	if !prior.IsNull() {
		proposed = pdv
		// proposed new state = prior with config values merged (enough here: config == prior for set args)
		pm := map[string]tftypes.Value{}
		_ = prior.As(&pm)
		cm := map[string]tftypes.Value{}
		_ = cfg.As(&cm)
		for k, v := range cm {
			if !v.IsNull() {
				pm[k] = v
			}
		}
		nv := tftypes.NewValue(h.ty, pm)
		proposed, _ = tfprotov6.NewDynamicValue(h.ty, nv)
	}
	resp, err := h.srv.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, PriorState: &pdv, Config: &cdv, ProposedNewState: &proposed})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("plan diag: %s: %s", d.Summary, d.Detail)
		}
	}
	pv, _ := resp.PlannedState.Unmarshal(h.ty)
	out := map[string]tftypes.Value{}
	_ = pv.As(&out)
	return out
}

func TestRestartDoesNotReapply(t *testing.T) {
	base := workerConfig(t)
	patch := "machine:\n  nodeLabels:\n    pool: gpu\n"
	cc := map[string]any{"ca_certificate": "Y2E=", "client_certificate": "Y2VydA==", "client_key": "a2V5"}
	cfgMap := map[string]any{"node": "10.0.0.2", "machine_configuration_input": base, "config_patches": []string{patch}, "client_configuration": cc}

	for _, tc := range []struct {
		name        string
		p           provider.Provider
		staleHash   bool
		wantCfgNull bool
	}{
		{"upstream: restart re-plans the rendered config", talos.New(), false, false},
		{"wrapped: restart with same hash plans nothing", New(), false, true},
		{"wrapped: changed render still plans the config", New(), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.p)
			cfg := h.val(t, cfgMap)
			// create plan: learn the hash the provider computes
			created := h.plan(t, tftypes.NewValue(h.ty, nil), cfg)
			var hash string
			_ = created["machine_configuration_hash"].As(&hash)
			if hash == "" {
				t.Fatal("no hash in create plan")
			}
			if tc.staleHash {
				hash = "0000"
			}
			// prior state as upjet rebuilds it after a restart: status.atProvider
			// (no machine_configuration) + sensitive params from secret refs
			prior := map[string]any{"id": "machine_configuration_apply", "node": "10.0.0.2", "endpoint": "10.0.0.2",
				"apply_mode": "auto", "resolved_apply_mode": "auto", "machine_configuration_input": base,
				"config_patches": []string{patch}, "client_configuration": cc, "machine_configuration_hash": hash}
			planned := h.plan(t, h.val(t, prior), cfg)
			gotNull := planned["machine_configuration"].IsNull()
			if gotNull != tc.wantCfgNull {
				t.Fatalf("machine_configuration null=%v, want %v", gotNull, tc.wantCfgNull)
			}
		})
	}
}
