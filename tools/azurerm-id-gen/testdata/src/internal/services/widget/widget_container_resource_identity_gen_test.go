// The identity test of a child whose parent is stated as the parent's id
// (#2138).

package widget_test

func TestAccWidgetContainer_resourceIdentity(t *testing.T) {
	data.ResourceIdentityTest(t, []acceptance.TestStep{
		{
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_widget_container.test", tfjsonpath.New("name"), tfjsonpath.New("name")),
				customstatecheck.ExpectStateContainsIdentityValueAtPath("azurerm_widget_container.test", tfjsonpath.New("resource_group_name"), tfjsonpath.New("storage_account_id")),
				customstatecheck.ExpectStateContainsIdentityValueAtPath("azurerm_widget_container.test", tfjsonpath.New("storage_account_name"), tfjsonpath.New("storage_account_id")),
				customstatecheck.ExpectStateContainsIdentityValueAtPath("azurerm_widget_container.test", tfjsonpath.New("subscription_id"), tfjsonpath.New("storage_account_id")),
			},
		},
	}, false)
}
