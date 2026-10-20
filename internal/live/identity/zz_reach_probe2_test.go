package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestZZReachProbe2(t *testing.T) {
	schemas := azureSchemas()
	schemas["azurerm_subnet_network_security_group_association"] = azurermSchema("subnet_id", "network_security_group_id")
	for _, live := range []string{"", "live {\n estate = \"e\"\n }"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
  }
  `+live+`
}
`+azureProviders+`
resource "azurerm_subnet_network_security_group_association" "a" {
  subnet_id                 = "/subscriptions/`+azureSub+`/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v/subnets/s"
  network_security_group_id = "/subscriptions/`+azureSub+`/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/n"
}
`), 0o600)
		cfg := loadConfig(t, dir, nil)
		result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		t.Logf("live=%q diags=%v", live, diags.ErrWithWarnings())
		if result != nil {
			for _, r := range result.All() {
				t.Logf("%s -> %s %s", r.Addr, r.Class, r.ImportID)
			}
		}
	}
}
