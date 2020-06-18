#!/bin/bash

# Incoming variables:
#   $1 - name of the manifest json file (should exist)
#   $2 - snapshot tag (just the date part; i.e. 2020-05-01-02-47-45)
#   $3 - sequence of events (1=endpoint operator, 2=hub operator)
# Required environment variables:
#   $QUAY_TOKEN - you know, the token... to quay (needs to be able to read open-cluster-management stuffs
#

if [[ -z "$QUAY_TOKEN" ]]
then
  echo "Please export QUAY_TOKEN"
  exit 1
fi

manifest_filename=$1
DATE_TAG=$2
ENDPOINT=$3

EP_VERS=`jq -r '(.[] | select (.["image-name"] == "endpoint-operator") | .["image-version"])' $1`
MCHO_VERS=`jq -r '(.[] | select (.["image-name"] == "multiclusterhub-operator") | .["image-version"])' $1`

echo Incoming manfiest filename: $manifest_filename
echo Incoming date tag: $DATE_TAG
echo Endpoint version: $EP_VERS
echo MCHO version: $MCHO_VERS
echo ENDPOINT: $ENDPOINT

if [[ "$ENDPOINT" == "1" ]]
then
  # The endpoint operator needs to exist first so later we can get its sha and insert that into hub operator
  ep_quaysha=`make -s retag/getquaysha RETAG_QUAY_COMPONENT_TAG=$EP_VERS-SNAPSHOT-$DATE_TAG COMPONENT_NAME=endpoint-operator`
  mco_quaysha=`make -s retag/getquaysha RETAG_QUAY_COMPONENT_TAG=$MCHO_VERS-SNAPSHOT-$DATE_TAG COMPONENT_NAME=multiclusterhub-operator`
  echo first round, endpoint-operator quay sha: $ep_quaysha
  echo first round, multiclusterhub-operator quay sha: $mco_quaysha
else
  # Now that endpoint operator exists, grab its sha so we have that in the manifest to insert into hub operator
  ep_quaysha=`make -s retag/getquaysha RETAG_QUAY_COMPONENT_TAG=$EP_VERS-SNAPSHOT-$DATE_TAG COMPONENT_NAME=endpoint-operator`
  mco_quaysha=`make -s retag/getquaysha RETAG_QUAY_COMPONENT_TAG=$MCHO_VERS-SNAPSHOT-$DATE_TAG COMPONENT_NAME=multiclusterhub-operator`
  echo second round, endpoint-operator quay sha: $ep_quaysha
  echo second round, multiclusterhub-operator quay sha: $mco_quaysha
fi
jq --arg ep_quaysha $ep_quaysha '(.[] | select (.["image-name"] == "endpoint-operator") | .["image-digest"]) |= $ep_quaysha' $manifest_filename > tmp.json ; mv tmp.json $manifest_filename
jq --arg mco_quaysha $mco_quaysha '(.[] | select (.["image-name"] == "multiclusterhub-operator") | .["image-digest"]) |= $mco_quaysha' $manifest_filename > tmp.json ; mv tmp.json $manifest_filename
if [[ "$ep_quaysha" == "null" || "$mco_quaysha" == "null" ]]; then echo Oh no - one of the operator image digests is missing!; exit 1; fi
echo After, $manifest_filename is:
cat $manifest_filename
