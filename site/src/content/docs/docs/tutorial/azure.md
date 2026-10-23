---
title: "Tutorial: Azure"
description: Apply a small estate to a local Azure emulator, delete its state cache, and watch the next plan find everything by its tags.
prompt: >-
  Follow https://intentius.io/choudoufu/docs/tutorial/azure/ on this machine.
  Run each step against the local floci-az emulator on localhost:4577 only,
  with the provider block exactly as the page shows it, and show me each
  step's check. Stop and ask before any command whose provider block, endpoint
  or credential is not the page's.
  Never apply or destroy (`choudoufu apply`, `choudoufu destroy`, `tofu apply`) against a real Azure subscription, approve a saved plan, or merge; never edit or remove an owner tag by hand.
---

You apply a resource group and a storage account to a local Azure emulator,
delete the state cache, and plan again from the tags alone. You need Docker,
`curl` and `openssl`.

## 1. Install choudoufu

Install the release binary as in [Start a new estate](../../use/start/#install).
Check it with `choudoufu version`.

## 2. Make a certificate

The emulator serves HTTPS on a certificate you trust once:

```
curl -fsSLO https://raw.githubusercontent.com/INTENTIUS/choudoufu/main/scripts/floci-az-mac-cert.sh
bash floci-az-mac-cert.sh
```

On macOS, trust it in your keychain:

```
security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db ~/.config/choudoufu/floci-az-tls/cert.pem
bash floci-az-mac-cert.sh --check
```

The check prints a line ending in `is ready`.

On Linux, add it to a bundle for this shell:

```
cat /etc/ssl/certs/ca-certificates.crt ~/.config/choudoufu/floci-az-tls/cert.pem > ~/.config/choudoufu/floci-az-tls/bundle.pem
export SSL_CERT_FILE=~/.config/choudoufu/floci-az-tls/bundle.pem
```

## 3. Start the emulator

```
docker run -d --name floci-az -p 127.0.0.1:4577:4577 \
  -e FLOCI_AZ_TLS_ENABLED=true \
  -e FLOCI_AZ_STORAGE_MODE=memory \
  -e FLOCI_AZ_PARITY=azure \
  -e FLOCI_AZ_STORAGE_SUFFIX=localhost:4577 \
  -e FLOCI_AZ_TLS_CERT_PATH=/etc/floci-az-tls/cert.pem \
  -e FLOCI_AZ_TLS_KEY_PATH=/etc/floci-az-tls/key.pem \
  -v "$HOME/.config/choudoufu/floci-az-tls/cert.pem:/etc/floci-az-tls/cert.pem:ro" \
  -v "$HOME/.config/choudoufu/floci-az-tls/key.pem:/etc/floci-az-tls/key.pem:ro" \
  ghcr.io/lex00/floci-az@sha256:c56ce3e2fb0da9a554cfb6794df912b371c27b1448c979ff2fd4d54cb8d47cd9
```

Wait until `curl -fsS http://localhost:4577/ready` succeeds.

## 4. Write the estate

Make a directory `alpha` and put three files in it.

`provider.tf` names the emulator for every endpoint and signs in with a fake
client secret, so nothing reaches Azure or your `az login`:

```hcl
terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.10.0"
    }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = "localhost:4577"
  subscription_id                 = "00000000-0000-0000-0000-000000000001"
  tenant_id                       = "00000000-0000-0000-0000-000000000002"
  client_id                       = "00000000-0000-0000-0000-000000000003"
  client_secret                   = "floci-az-does-not-check-this"
  use_cli                         = false
  use_msi                         = false
  use_oidc                        = false
  use_aks_workload_identity       = false
  resource_provider_registrations = "none"
  disable_terraform_partner_id    = true
}
```

`main.tf`:

```hcl
resource "azurerm_resource_group" "app" {
  name     = "alpha-rg"
  location = "eastus"
}

resource "azurerm_storage_account" "app" {
  name                     = "alphastore"
  resource_group_name      = azurerm_resource_group.app.name
  location                 = azurerm_resource_group.app.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}
```

`estate.chdf.hcl`, the live backend:

```hcl
estate = "alpha"
```

## 5. Init

In `alpha`:

```
choudoufu init
```

It prints `OpenTofu has been successfully initialized!`

## 6. Apply

```
choudoufu apply
```

The plan adds `tofu-estate` and `tofu-address` to both tag maps. Type `yes`.
The apply ends with `Apply complete!`

## 7. See the tags

Ask the emulator for the resource group:

```
curl -fsS --cacert ~/.config/choudoufu/floci-az-tls/cert.pem \
  -H "Authorization: Bearer floci-az" \
  "https://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/alpha-rg?api-version=2021-04-01"
```

Its `tags` hold `"tofu-estate": "alpha"` and
`"tofu-address": "azurerm_resource_group.app"`.

## 8. Delete the cache

```
rm .terraform/choudoufu-cache.tfstate
```

## 9. Plan from the tags

```
choudoufu plan
```

The plan finds both resources by their tags and prints `No changes.`

## 10. A second estate

Make a directory `beta` beside `alpha`, copy `alpha/provider.tf` into it, and
add two files.

`main.tf`:

```hcl
resource "azurerm_resource_group" "app" {
  name     = "beta-rg"
  location = "eastus"
}
```

`estate.chdf.hcl`:

```hcl
estate = "beta"
```

In `beta`, run `choudoufu init` and `choudoufu plan`. The plan adds `beta-rg`
and leaves alpha's resources alone. Apply it, then run `choudoufu plan` in
`alpha` again. It prints `No changes.`

Stop the emulator with `docker rm -f floci-az`.

## Next

- [Start a new estate](../../use/start/)
- [Migrate an existing estate](../../use/migrate/)
- [Azure](../../../azure/)
