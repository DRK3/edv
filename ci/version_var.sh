
#
# Copyright SecureKey Technologies Inc. All Rights Reserved.
#
# SPDX-License-Identifier: Apache-2.0
#


# Release Parameters
BASE_VERSION=0.1.6
IS_RELEASE=false

# Project Parameters
SOURCE_REPO=edv
BASE_PKG_NAME=edv-rest
RELEASE_REPO=docker.pkg.github.com/trustbloc/${SOURCE_REPO}
SNAPSHOT_REPO=docker.pkg.github.com/trustbloc-cicd/snapshot

if [ ${IS_RELEASE} = false ]
then
  EXTRA_VERSION=snapshot-$(git rev-parse --short=7 HEAD)
  PROJECT_VERSION=${BASE_VERSION}-${EXTRA_VERSION}
  PROJECT_PKG_REPO=${SNAPSHOT_REPO}
else
  PROJECT_VERSION=${BASE_VERSION}
  PROJECT_PKG_REPO=${RELEASE_REPO}
fi

export EDV_REST_TAG=$PROJECT_VERSION
export EDV_REST_PKG=${PROJECT_PKG_REPO}/${BASE_PKG_NAME}
