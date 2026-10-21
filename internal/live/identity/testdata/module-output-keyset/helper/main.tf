# Verbatim from terraform-google-modules/iam v8.3.0, modules/helper/main.tf,
# variables.tf and outputs.tf, concatenated; only this header is added.
# Apache License 2.0, notice below.

/**
 * Copyright 2023 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

locals {
  authoritative = var.mode == "authoritative"
  additive      = var.mode == "additive"

  # When there is only one entity, consider that the entity passed
  # might be dynamic. In this case the `for_each` will not use
  # entity name when constructing the unique ID.
  #
  # Other rules regrading the dynamic nature of resources:
  # 1. The roles might never be dynamic.
  # 2. Members might only be dynamic in `authoritative` mode.
  singular = length(var.entities) == 1

  # In singular mode, replace entity name with a constant "default". This
  # will prevent the potentially dynamic resource name usage in the `for_each`
  aliased_entities = local.singular ? ["default"] : var.entities

  # Values in the map need to be the proper entity names
  real_entities = var.entities

  bindings_by_role = distinct(flatten([
    for name in local.real_entities
    : [
      for role, members in var.bindings
      : { name = name, role = role, members = members, condition = { title = "", description = "", expression = "" } }
    ]
  ]))

  bindings_by_conditions = distinct(flatten([
    for name in local.real_entities
    : [
      for binding in var.conditional_bindings
      : { name = name, role = binding.role, members = binding.members, condition = { title = binding.title, description = binding.description, expression = binding.expression } }
    ]
  ]))

  all_bindings = concat(local.bindings_by_role, local.bindings_by_conditions)

  bindings_by_member = distinct(flatten([
    for binding in local.all_bindings
    : [
      for member in binding["members"]
      : { name = binding["name"], role = binding["role"], member = member, condition = binding["condition"] }
    ]
  ]))

  keys_authoritative = distinct(flatten([
    for alias in local.aliased_entities
    : [
      for role in keys(var.bindings)
      : "${alias}--${role}"
    ]
  ]))

  keys_authoritative_conditional = distinct(flatten([
    for alias in local.aliased_entities
    : [
      for binding in var.conditional_bindings
      : "${alias}--${binding.role}--${binding.title}"
    ]
  ]))

  keys_additive = distinct(flatten([
    for alias in local.aliased_entities
    : [
      for role, members in var.bindings
      : [
        for member in members
        : "${alias}--${role}--${member}"
      ]
    ]
  ]))

  keys_additive_conditional = distinct(flatten([
    for alias in local.aliased_entities
    : [
      for binding in var.conditional_bindings
      : [
        for member in binding.members
        : "${alias}--${binding.role}--${binding.title}--${member}"
      ]
    ]
  ]))

  all_keys_authoritative = concat(local.keys_authoritative, local.keys_authoritative_conditional)

  all_keys_additive = concat(local.keys_additive, local.keys_additive_conditional)

  bindings_authoritative = (
    local.authoritative
    ? zipmap(local.all_keys_authoritative, local.all_bindings)
    : {}
  )

  bindings_additive = (
    local.additive
    ? zipmap(local.all_keys_additive, local.bindings_by_member)
    : {}
  )

  # It is important to provide a set for the `for_each` instead of
  # the map, since we have to guarantee that the `for_each`
  # expression is resolved synchonously.
  set_authoritative = (
    local.authoritative
    ? toset(local.all_keys_authoritative)
    : []
  )

  set_additive = (
    local.additive
    ? toset(local.all_keys_additive)
    : []
  )
}

/**
 * Copyright 2023 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

variable "bindings" {
  description = "Map of role (key) and list of members (value) to add the IAM policies/bindings"
  type        = map(list(string))
  default     = {}
}

variable "mode" {
  description = "Mode for adding the IAM policies/bindings, additive and authoritative"
  type        = string
  default     = "additive"
}

variable "entities" {
  description = "Entities list to add the IAM policies/bindings"
  type        = list(string)
}

variable "conditional_bindings" {
  description = "List of maps of role and respective conditions, and the members to add the IAM policies/bindings"
  type = list(object({
    role        = string
    title       = string
    description = string
    expression  = string
    members     = list(string)
  }))
  default = [
  ]
}

/**
 * Copyright 2023 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

output "bindings_by_member" {
  value       = local.bindings_by_member
  description = "List of bindings for entities unwinded by members."
}

output "set_authoritative" {
  value       = local.set_authoritative
  description = "A set of authoritative binding keys (from bindings_authoritative) to be used in for_each. Unwinded by roles."
}

output "set_additive" {
  value       = local.set_additive
  description = "A set of additive binding keys (from bindings_additive) to be used in for_each. Unwinded by members."
}

output "bindings_authoritative" {
  value       = local.bindings_authoritative
  description = "Map of authoritative bindings for entities. Unwinded by roles."
}

output "bindings_additive" {
  value       = local.bindings_additive
  description = "Map of additive bindings for entities. Unwinded by members."
}
