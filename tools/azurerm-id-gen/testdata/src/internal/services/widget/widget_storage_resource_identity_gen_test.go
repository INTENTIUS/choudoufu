// A miniature of the resource identity test azurerm generates per type
// (internal/tools/generator-tests resourceidentity).

package widget_test

func TestAccWidgetStorage_resourceIdentity(t *testing.T) {
	data := acceptance.BuildTestData(t, "azurerm_widget_storage", "test")
	r := WidgetStorageResource{}

	data.ResourceIdentityTest(t, []acceptance.TestStep{
		{
			Config: r.basic(data),
			ConfigStateChecks: []statecheck.StateCheck{
				customstatecheck.ExpectAllIdentityFieldsAreChecked("azurerm_widget_storage.test", checkedFields),
				statecheck.ExpectIdentityValue("azurerm_widget_storage.test", tfjsonpath.New("subscription_id"), knownvalue.StringExact(data.Subscriptions.Primary)),
				statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_widget_storage.test", tfjsonpath.New("name"), tfjsonpath.New("name")),
				statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_widget_storage.test", tfjsonpath.New("resource_group_name"), tfjsonpath.New("resource_group_name")),
			},
		},
	}, false)
}
