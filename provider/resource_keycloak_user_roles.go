package provider

import (
	"fmt"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/mrparkers/terraform-provider-keycloak/keycloak"
	"strings"
)

func resourceKeycloakUserRoles() *schema.Resource {
	return &schema.Resource{
		Create: resourceKeycloakUserRolesCreate,
		Read:   resourceKeycloakUserRolesRead,
		Update: resourceKeycloakUserRolesUpdate,
		Delete: resourceKeycloakUserRolesDelete,
		// This resource can be imported using {{realm}}/{{userId}}.
		Importer: &schema.ResourceImporter{
			State: resourceKeycloakUserRolesImport,
		},
		Schema: map[string]*schema.Schema{
			"realm_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"user_id": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},
			"role_ids": {
				Type:     schema.TypeSet,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
				Required: true,
			},
		},
	}
}

func userRolesId(realmId, userId string) string {
	return fmt.Sprintf("%s/%s", realmId, userId)
}

// convert the UserRoleMapping struct into a realm-/client-id-to-role map
func getMapOfRealmAndClientRolesFromUser(roleMappings *keycloak.UserRoleMapping) (map[string][]*keycloak.Role, error) {
	roles := make(map[string][]*keycloak.Role)

	if len(roleMappings.RealmMappings) != 0 {
		roles["realm"] = roleMappings.RealmMappings
	}

	for _, clientRoleMapping := range roleMappings.ClientMappings {
		roles[clientRoleMapping.Id] = clientRoleMapping.Mappings
	}

	return roles, nil
}

func addRolesToUser(keycloakClient *keycloak.KeycloakClient, rolesToAdd map[string][]*keycloak.Role, user *keycloak.User) error {
	if realmRoles, ok := rolesToAdd["realm"]; ok && len(realmRoles) != 0 {
		err := keycloakClient.AddRealmRolesToUser(user.RealmId, user.Id, realmRoles)
		if err != nil {
			return err
		}
	}

	for k, roles := range rolesToAdd {
		if k == "realm" {
			continue
		}

		err := keycloakClient.AddClientRolesToUser(user.RealmId, user.Id, k, roles)
		if err != nil {
			return err
		}
	}

	return nil
}

func removeRolesFromUser(keycloakClient *keycloak.KeycloakClient, rolesToRemove map[string][]*keycloak.Role, user *keycloak.User) error {
	if realmRoles, ok := rolesToRemove["realm"]; ok && len(realmRoles) != 0 {
		err := keycloakClient.RemoveRealmRolesFromUser(user.RealmId, user.Id, realmRoles)
		if err != nil {
			return err
		}
	}

	for k, roles := range rolesToRemove {
		if k == "realm" {
			continue
		}

		err := keycloakClient.RemoveClientRolesFromUser(user.RealmId, user.Id, k, roles)
		if err != nil {
			return err
		}
	}

	return nil
}

func resourceKeycloakUserRolesCreate(data *schema.ResourceData, meta interface{}) error {
	keycloakClient := meta.(*keycloak.KeycloakClient)

	realmId := data.Get("realm_id").(string)
	userId := data.Get("user_id").(string)

	user, err := keycloakClient.GetUser(realmId, userId)
	if err != nil {
		return err
	}

	roleIds := interfaceSliceToStringSlice(data.Get("role_ids").(*schema.Set).List())
	tfRoles, err := getMapOfRealmAndClientRoles(keycloakClient, realmId, roleIds)
	if err != nil {
		return err
	}

	// get the list of currently assigned roles. Due to default-realm- and client-roles
	// (e.g. roles of the account-client) this is probably not empty upon resource creation
	roleMappings, err := keycloakClient.GetUserRoleMappings(realmId, userId)
	remoteRoles, err := getMapOfRealmAndClientRolesFromUser(roleMappings)
	if err != nil {
		return err
	}

	// sort into roles we need to add and roles we need to remove
	removeDuplicateRoles(&tfRoles, &remoteRoles)

	// add roles
	err = addRolesToUser(keycloakClient, tfRoles, user)
	if err != nil {
		return err
	}

	// remove roles
	err = removeRolesFromUser(keycloakClient, remoteRoles, user)
	if err != nil {
		return err
	}

	data.SetId(userRolesId(realmId, userId))
	return resourceKeycloakUserRolesRead(data, meta)
}

func resourceKeycloakUserRolesRead(data *schema.ResourceData, meta interface{}) error {
	keycloakClient := meta.(*keycloak.KeycloakClient)

	realmId := data.Get("realm_id").(string)
	userId := data.Get("user_id").(string)

	roles, err := keycloakClient.GetUserRoleMappings(realmId, userId)
	if err != nil {
		return err
	}

	var roleIds []string

	for _, realmRole := range roles.RealmMappings {
		roleIds = append(roleIds, realmRole.Id)
	}

	for _, clientRoleMapping := range roles.ClientMappings {
		for _, clientRole := range clientRoleMapping.Mappings {
			roleIds = append(roleIds, clientRole.Id)
		}
	}

	data.Set("role_ids", roleIds)
	data.SetId(userRolesId(realmId, userId))

	return nil
}

func resourceKeycloakUserRolesUpdate(data *schema.ResourceData, meta interface{}) error {
	keycloakClient := meta.(*keycloak.KeycloakClient)

	realmId := data.Get("realm_id").(string)
	userId := data.Get("user_id").(string)

	user, err := keycloakClient.GetUser(realmId, userId)
	if err != nil {
		return err
	}

	roleIds := interfaceSliceToStringSlice(data.Get("role_ids").(*schema.Set).List())
	tfRoles, err := getMapOfRealmAndClientRoles(keycloakClient, realmId, roleIds)
	if err != nil {
		return err
	}

	roleMappings, err := keycloakClient.GetUserRoleMappings(realmId, userId)
	remoteRoles, err := getMapOfRealmAndClientRolesFromUser(roleMappings)
	if err != nil {
		return err
	}

	removeDuplicateRoles(&tfRoles, &remoteRoles)

	// `tfRoles` contains all roles that need to be added
	// `remoteRoles` contains all roles that need to be removed

	err = addRolesToUser(keycloakClient, tfRoles, user)
	if err != nil {
		return err
	}

	err = removeRolesFromUser(keycloakClient, remoteRoles, user)
	if err != nil {
		return err
	}

	return nil
}

func resourceKeycloakUserRolesDelete(data *schema.ResourceData, meta interface{}) error {
	keycloakClient := meta.(*keycloak.KeycloakClient)

	realmId := data.Get("realm_id").(string)
	userId := data.Get("user_id").(string)

	user, err := keycloakClient.GetUser(realmId, userId)

	roleIds := interfaceSliceToStringSlice(data.Get("role_ids").(*schema.Set).List())
	rolesToRemove, err := getMapOfRealmAndClientRoles(keycloakClient, realmId, roleIds)
	if err != nil {
		return err
	}

	err = removeRolesFromUser(keycloakClient, rolesToRemove, user)
	if err != nil {
		return err
	}

	return nil
}

func resourceKeycloakUserRolesImport(d *schema.ResourceData, _ interface{}) ([]*schema.ResourceData, error) {
	parts := strings.Split(d.Id(), "/")

	if len(parts) != 2 {
		return nil, fmt.Errorf("Invalid import. Supported import format: {{realm}}/{{userId}}.")
	}

	d.Set("realm_id", parts[0])
	d.Set("user_id", parts[1])

	d.SetId(userRolesId(parts[0], parts[1]))

	return []*schema.ResourceData{d}, nil
}