# One GCP estate's grant (#1121; live/MARKERS.md, "Granting a GCP
# estate"): the project IAM bindings that hand one estate to the principal
# that runs it. An IAM condition cannot read a label, so the fence is the
# project: one estate per project, and holding the estate is holding these
# bindings on its project. Replace PROJECT, PRINCIPAL and ROLE, then run
# the lines as someone who may enable services and set the project's IAM
# policy. Handover is these bindings moving from one principal to
# another; nothing on the resources changes.
#
# PRINCIPAL is an IAM member string, such as
# serviceAccount:runner@PROJECT.iam.gserviceaccount.com. ROLE is a role
# the estate's declared types need to be created, changed and deleted
# (roles/compute.networkAdmin, roles/storage.admin, ...): repeat the last
# binding once per role. The first two lines are the estate sweep's: the
# Cloud Asset API enabled in the project, and
# cloudasset.assets.searchAllResources on it, which
# roles/cloudasset.viewer carries.
#
# Two estates sharing one project are not fenced from each other: each
# principal holds the other estate's resources too. A binding on the
# project's folder or organization inherits into the project and passes
# this fence the way an account's root passes an IAM condition on AWS.
gcloud services enable cloudasset.googleapis.com --project=PROJECT
gcloud projects add-iam-policy-binding PROJECT --member=PRINCIPAL --role=roles/cloudasset.viewer --condition=None
gcloud projects add-iam-policy-binding PROJECT --member=PRINCIPAL --role=ROLE --condition=None
