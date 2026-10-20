// The identity test of a record set: a renamed argument and a constant
// (#2138).

package widget_test

func TestAccWidgetRecord_resourceIdentity(t *testing.T) {
	data.ResourceIdentityTest(t, []acceptance.TestStep{
		{
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_widget_record.test", tfjsonpath.New("dns_zone_name"), tfjsonpath.New("zone_name")),
				statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_widget_record.test", tfjsonpath.New("name"), tfjsonpath.New("name")),
				statecheck.ExpectIdentityValue("azurerm_widget_record.test", tfjsonpath.New("record_type"), knownvalue.StringExact("CNAME")),
				statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_widget_record.test", tfjsonpath.New("resource_group_name"), tfjsonpath.New("resource_group_name")),
				statecheck.ExpectIdentityValue("azurerm_widget_record.test", tfjsonpath.New("subscription_id"), knownvalue.StringExact(data.Subscriptions.Primary)),
			},
		},
	}, false)
}
