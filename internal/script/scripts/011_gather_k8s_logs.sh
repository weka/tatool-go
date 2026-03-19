#!/bin/bash

# === Setup working directory ===
WORKDIR="$(mktemp -d)"
trap "rm -rf $WORKDIR" EXIT

# === Write gather_k8s_logs.sh ===
cat > "$WORKDIR/gather_k8s_logs.sh" <<'EOF'
#!/usr/bin/env bash

# Bug:  Not using output dir arg correctly (Fixed)
# RFE:  Show more output when running kubectl 
# Bug:  Don't get pods when running with --no-dump

# Function to source a config file and set environment variables
WEKACLUSTER_NAMESPACE="default"
WEKACLIENT_NAMESPACE="default"
OPERATOR_NAMESPACE="weka-operator-system"
CSI_NAMESPACE="csi-wekafs"

# Common Customer Setup
#WEKACLUSTER_NAMESPACE="weka-operator-system"
#WEKACLIENT_NAMESPACE="weka-operator-system"
#OPERATOR_NAMESPACE="weka-operator-system"
#CSI_NAMESPACE="csi-wekafs"

# mylog=gather-logs.$(date +%Y-%m-%d_%H-%M-%S).log
# exec > >(tee -i $mylog)
# exec 2>&1

# Assumptions
#   bash in your environment and PATH
#   kubectl in your enviornment and PATH

# Config values
# Future enhancement: detect OS and set logfile_array accordingly.  It will differ for Ubuntu vs RHEL for example.  Maybe leverage "WEKA Diagnostics"
# Future enhancement: allow wildcards in logfile_array
logfile_array=("/var/log/error" "/var/log/syslog" "/proc/wekafs/interface")
weka_cmd_array=("weka events -n 10000" "weka status")

# Default values
dev_mode=false
output_dir="./logs/"$(date +%Y-%m-%d_%H-%M-%S)
config_file="./config.env"
since="1h"
tail_lines="100"
if [ -n "$KUBECONFIG" ]; then
  kubeconfig=$KUBECONFIG
else
  kubeconfig=~/.kube/config
fi
dump_cluster=true
wekacluster_cli=true

# Function to display usage information
usage() {
  echo "Usage: $0 [-c | --config <config_file>] [-o | --output <output_dir>] [-s | --since <since>] [-t | --tail <tail_lines>] [-k | --kubeconfig <kubeconfig>] [--no-dump] [--no-wekacluster-cli] [-h | --help]"
  echo "  -c, --config <config_file>      : Path to the config file (default: ./config.env. Note: A config file is required)"
  echo "  -o, --output <output_dir>       : Output directory for logs (default: $output_dir)"
  echo "  -s, --since <since>             : Time duration for logs (e.g., 1h, 30m, 1d) (default: $since)"
  echo "  -t, --tail <tail_lines>         : Number of lines to tail from the end of the logs (default: $tail_lines)"
  echo "  -k, --kubeconfig <kubeconfig>   : Path to the kubeconfig file.  Can be set by env variable KUBECONFIG. (default: $kubeconfig)"
  echo "  --no-dump                       : Do not run kubectl cluster-info dump. Useful on large clusters with many non-WEKA items. (default: run cluster-info dump)"
  echo "  --no-wekacluster-cli            : Do not run WekaCluster API calls.  Useful if WEKA API non-responsive. (default: run WekaCluster API calls)"
  echo "  -h, --help                      : Display this help message"
  echo "  --devmode                       : Run in devmode (default: false)"
  exit 1
}


#Process arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    -c|--config)
      config_file="$2"
      shift 2
      ;;
    -o|--output)
      output_dir="$2"
      shift 2
      ;;    
    -s|--since)
      since="$2"
      shift 2
      ;;
    -t|--tail)
      tail_lines="$2"
      shift 2
      ;;
    -k|--kubeconfig)
      kubeconfig="$2"
      shift 2
      ;;
    --no-dump)
      dump_cluster=false
      shift
      ;;
    --no-wekacluster-cli)
      wekacluster_cli=true
      shift
      ;;
    --devmode)
      dev_mode=true
      shift
      ;;
    -h|--help)
      usage
      shift
      ;;
    *)
      echo "Unknown option: $1"
      usage
      ;;
  esac
done
shift $((OPTIND-1))

cluster_dump_dir=$output_dir/cluster-info  #Use the K8s cluster-info structure, even if we don't run `kubectl cluster-info dump`

# Source the config file if it exists
#if [[ -f "$config_file" ]]; then
#  source_config "$config_file"
#fi

# Source the config file (it must exist)
# Comment this out included in script itself
#source_config "$config_file"
operator_namespace=$OPERATOR_NAMESPACE
wekacluster_namespace=$WEKACLUSTER_NAMESPACE
wekaclient_namespace=$WEKACLIENT_NAMESPACE
csi_namespace=$CSI_NAMESPACE
    
if [[ "$dev_mode" = "true" ]]; then
  echo "Dev mode is on"
  output_dir="./logs/dev"  #Allows you to reuse the same directory over and over
  rm -r "$output_dir"
  cluster_dump_dir=$output_dir/cluster-info
fi

# Function to dump cluster info for WEKA namespaces
dump_cluster_info() {
  echo "Gathering cluster info..."
  echo "Running command: kubectl --kubeconfig $kubeconfig cluster-info dump --output-directory $cluster_dump_dir -ojson --namespaces $operator_namespace,$wekacluster_namespace,$wekaclient_namespace,$csi_namespace"
  kubectl --kubeconfig $kubeconfig cluster-info dump --output-directory $cluster_dump_dir -ojson --namespaces $operator_namespace,$wekacluster_namespace,$wekaclient_namespace,$csi_namespace
  if [ $? -ne 0 ]; then
    echo "Error getting cluster info"
  fi
}

# Function to get all namespaces
dump_namespaces() {
  local namespaces_file="$cluster_dump_dir/all_namespaces.json"
  echo "Gathering namespaces..."
  echo "Running command: kubectl --kubeconfig $kubeconfig get namespaces"
  kubectl --kubeconfig "$kubeconfig" get namespaces -ojson > "$namespaces_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error getting namespaces"
  fi
}

# Function to dump all events
dump_events() {
  local events_file="$cluster_dump_dir/events.json"
  echo "Gathering events..."
  echo "Running command: kubectl --kubeconfig $kubeconfig get events --all-namespaces"
  kubectl --kubeconfig "$kubeconfig" get events -ojson --all-namespaces > "$events_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error getting events"
  fi
}

# Function to dump all persistent volumes
dump_pvs() {
  local pvs_file="$cluster_dump_dir/persistent_volumes.json"
  echo "Gathering persistent volumes..."
  echo "Running command: kubectl --kubeconfig $kubeconfig get pv"
  kubectl --kubeconfig "$kubeconfig" get pv -ojson > "$pvs_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error getting persistent volumes"
  fi
}

# Function to dump all persistent volume claims
dump_pvcs() {
  local pvcs_file="$cluster_dump_dir/persistent_volume_claims.json"
  echo "Gathering persistent volume claims..."
  echo "Running command: kubectl --kubeconfig $kubeconfig get pvc --all-namespaces"
  kubectl --kubeconfig "$kubeconfig" get pvc -ojson --all-namespaces > "$pvcs_file"
  if [ $? -ne 0 ]; then
    echo "Error getting persistent volume claims"
  fi
}



# Function to get logs for one container in one pod
get_pod_logs() {
  local pod_name="$1"
  local container_name="$2"
  local pod_namespace="$3"
  local log_file="$cluster_dump_dir/$pod_namespace/$pod/${pod_name}_${container_name}.log"

  mkdir -p $(dirname $log_file)
  
  echo "Gathering logs for pod: $pod_name, container: $container_name"
  echo "Running command: kubectl --kubeconfig $kubeconfig logs $pod_name -n $pod_namespace -c $container_name --since=$since --tail=$tail_lines"
  kubectl --kubeconfig "$kubeconfig" logs "$pod_name" -n "$pod_namespace" -c "$container_name" --since="$since" --tail="$tail_lines" > "$log_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error getting logs for pod: $pod_name, container: $container_name, namespace: $pod_namespace"
  fi
}

# Function to desribe one pod
describe_pod() {
  local pod_name="$1"
  local pod_namespace="$2"
  local pod_file="$cluster_dump_dir/$pod_namespace/$pod/$pod_${pod_name}.describe"

  mkdir -p $(dirname $pod_file)
  
  echo "Describing pod: $pod_name, namespace: $pod_namespace"
  echo "Running command: kubectl --kubeconfig $kubeconfig describe pod $pod_name -n $pod_namespace -o yaml"
  kubectl --kubeconfig "$kubeconfig" describe pod "$pod_name" -n "$pod_namespace" > "$pod_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error describing pod: $pod_name, namespace: $pod_namespace"
  fi
}


# Function to describe one node
describe_node() {
  local node_name="$1"
  local dirname=$cluster_dump_dir/nodes/$node_name
  local node_file="$dirname/node_${node_name}.describe"

  echo "Creating directory: $dirname"
  mkdir -p "$dirname"
  
  echo "Describing node: $node_name"
  echo "Running command: kubectl --kubeconfig $kubeconfig describe node $node_name"
  kubectl --kubeconfig "$kubeconfig" describe node "$node_name" > "$node_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error describing node: $node_name"
  fi
}

# Function to capture one logfile from one node
get_node_logfile() {
  local node_name="$1"
  local logfile="$2"
  local dirname2=$(dirname $logfile)
  local dirname=$cluster_dump_dir/nodes/$node_name$dirname2
  local basename=$(basename $logfile)
  local log_file="$dirname/$basename"

  
  echo "Creating directory: $dirname"
  mkdir -p "$dirname"

  echo "Gathering logfile: $logfile from node: $node_name"
  echo "Running command: kubectl --kubeconfig $kubeconfig debug node/$node_name -q --image=busybox --attach=true --target=host -- chroot /host cat $logfile"
  kubectl --kubeconfig "$kubeconfig" debug node/"$node_name" -q --image=busybox --attach=true --target=host -- chroot /host cat "$logfile" > "$log_file" 2>&1
  #  kubectl --kubeconfig kube-ranzo-20250311.yaml debug node/18.144.156.82 --attach=true --image=busybox -- ls

  if [ $? -ne 0 ]; then
    echo "Error getting logfile: $logfile from node: $node_name"
  fi
}

# Function to capture output from one 'weka' command run on one pod
run_wekacluster_cmd() {
  local pod_name="$1"
  local pod_namespace="$2"
  local cmd="$3"
  local log_file="$output_dir/wekacluster.txt"
  echo "Running command: $cmd, pod: $pod_name, namespace: $pod_namespace"
  echo "Running command: kubectl --kubeconfig $kubeconfig exec -n $pod_namespace $pod_name -- $cmd"
  kubectl --kubeconfig "$kubeconfig" exec -n $pod_namespace $pod_name -- $cmd >> "$log_file" 2>&1
  if [ $? -ne 0 ]; then
    echo "Error running command: $cmd"
    return 1
  fi
}

# Function to get all pods in a namespace
get_all_pods() {
  local namespace="$1"
  echo "Running command: kubectl --kubeconfig $kubeconfig get pods -n $namespace -o jsonpath='{.items[*].metadata.name}'"
  kubectl --kubeconfig "$kubeconfig" get pods -n "$namespace" -o jsonpath='{.items[*].metadata.name}'
  if [ $? -ne 0 ]; then 
    echo "Error getting pods in namespace: $namespace"
    exit 1
  fi
}

# Function to output all wekacontainers in a namespace
dump_wekacontainers() {
  local namespace="$1"
  local log_file="$cluster_dump_dir/$namespace/wekacontainer.json"

  mkdir -p $(dirname $log_file)
  echo "Gathering wekacontainers in namespace: $namespace"

  echo "Running command: kubectl --kubeconfig $kubeconfig get wekacontainers -n $namespace -ojson"
  kubectl --kubeconfig "$kubeconfig" get wekacontainers -n "$namespace" -ojson > "$log_file" 2>&1
  
  if [ $? -ne 0 ]; then
    echo "Error getting wekacontainers in namespace: $namespace"
    exit 1
  fi
}

# Function to output all wekaclusters in a namespace
dump_wekaclusters() {
  local namespace="$1"
  local log_file="$cluster_dump_dir/$namespace/wekacluster.json"

  mkdir -p $(dirname $log_file)
  echo "Gathering wekaclusters in namespace: $namespace"

  echo "Running command: kubectl --kubeconfig $kubeconfig get wekaclusters -n $namespace -ojson"
  kubectl --kubeconfig "$kubeconfig" get wekaclusters -n "$namespace" -ojson > "$log_file" 2>&1
  
  if [ $? -ne 0 ]; then
    echo "Error getting wekaclusters in namespace: $namespace"
    exit 1
  fi
}

# Function to output all wekaclients in a namespace
dump_wekaclients() {
  local namespace="$1"
  local log_file="$cluster_dump_dir/$namespace/wekaclient.json"

  mkdir -p $(dirname $log_file)
  echo "Gathering wekaclients in namespace: $namespace"

  echo "Running command: kubectl --kubeconfig $kubeconfig get wekaclients -n $namespace -ojson"
  kubectl --kubeconfig "$kubeconfig" get wekaclients -n "$namespace" -ojson > "$log_file" 2>&1
  
  if [ $? -ne 0 ]; then
    echo "Error getting wekaclients in namespace: $namespace"
    exit 1
  fi
}



# Function to clean up debugger pods
cleanup_debug_pods() {
  #echo "Cleaning up debug pods..."
  # echo "Running command: kubectl --kubeconfig $kubeconfig get pods -n default -o=custom-columns=NAME:.metadata.name | grep node-debugger | xargs -I {} kubectl --kubeconfig $kubeconfig delete pod -n default {}"
  # kubectl get pods -n default -o=custom-columns=NAME:.metadata.name | grep node-debugger | xargs -I {} kubectl delete pod -n default {}
  # ^^^ Some bug prevents this from working when called from the script but it works with zsh

 echo "Please review and run the following command: kubectl --kubeconfig $kubeconfig get pods -n default -o=custom-columns=NAME:.metadata.name | grep node-debugger | xargs -I {} kubectl --kubeconfig $kubeconfig delete pod -n default {}"

  
  if [ $? -ne 0 ]; then
    echo "Error cleaning up debug pods"
  fi
}

###

# Create output and cluster dump directory if it doesn't exist
mkdir -p "$output_dir"
mkdir -p "$cluster_dump_dir"


if [[ $dump_cluster == "true" ]]; then
  dump_cluster_info
fi

dump_namespaces
dump_events
dump_pvs
dump_pvcs

dump_wekacontainers "$operator_namespace"
dump_wekacontainers "$wekacluster_namespace"
dump_wekacontainers "$wekaclient_namespace"

dump_wekaclusters "$wekacluster_namespace"
dump_wekaclients "$wekaclient_namespace"

dump_wekacontainers "$operator_namespace"

# Get all nodes in the K8s cluster
nodes=$(kubectl --kubeconfig "$kubeconfig" get nodes -o jsonpath='{.items[*].metadata.name}')

# Get all compute pods in the WekaCluster namespace
compute_pods=$(kubectl --kubeconfig "$kubeconfig" get pods -n "$wekacluster_namespace" --selector weka.io/mode=compute -o jsonpath='{.items[*].metadata.name}')

# Get all drive pods in the WekaCluster namespace
drive_pods=$(kubectl --kubeconfig "$kubeconfig" get pods -n "$wekacluster_namespace" --selector weka.io/mode=drive -o jsonpath='{.items[*].metadata.name}')

# Get all client pods in the WekaClient namespace
client_pods=$(kubectl --kubeconfig "$kubeconfig" get pods -n "$wekaclient_namespace" --selector weka.io/mode=client -o jsonpath='{.items[*].metadata.name}')

# Get all pods in the Weka Operator namespace
operator_pods=$(kubectl --kubeconfig "$kubeconfig" get pods -n "$operator_namespace" -o jsonpath='{.items[*].metadata.name}')

# Get all pods in the CSI namespace
csi_pods=$(kubectl --kubeconfig "$kubeconfig" get pods -n "$csi_namespace" -o jsonpath='{.items[*].metadata.name}')



# If wekacluster_cli is true, run wekacluster commands for one pod
if [ "$wekacluster_cli" = "true" ]; then
  # Iterate over each compute pod in the WekaCluster namespace until one of them works
  for cmd in "${weka_cmd_array[@]}"; do
      for pod in $compute_pods; do
        run_wekacluster_cmd "$pod" "$wekacluster_namespace" "$cmd"
        if [ $? -eq 0 ]; then
          break
        fi
      done
  done
fi


# Only do this approach if $dump_cluster == "false"
if [[ $dump_cluster == "false" ]]; then
  # Iterate over each compute pod in the Weka Cluster namespace
  for pod in $compute_pods; do
    # Describe the pod and place in logs
    describe_pod "$pod" "$wekacluster_namespace"

    # Get all containers in the current pod
    containers=$(kubectl --kubeconfig "$kubeconfig" get pod "$pod" -n "$wekacluster_namespace" -o jsonpath='{.spec.containers[*].name}')

    # Iterate over each container in the current pod
    for container in $containers; do
      get_pod_logs "$pod" "$container" "$wekacluster_namespace"
    done
  done

  # Iterate over each drive pod in the Weka Cluster namespace
  for pod in $drive_pods; do
    # Describe the pod and place in logs
    describe_pod "$pod" "$wekacluster_namespace"

    # Get all containers in the current pod
    containers=$(kubectl --kubeconfig "$kubeconfig" get pod "$pod" -n "$wekacluster_namespace" -o jsonpath='{.spec.containers[*].name}')

    # Iterate over each container in the current pod
    for container in $containers; do
      get_pod_logs "$pod" "$container" "$wekacluster_namespace"
    done
  done

  # Iterate over each client pod in the Weka Client namespace
  for pod in $client_pods; do
    # Describe the pod and place in logs
    describe_pod "$pod" "$wekaclient_namespace"

    # Get all containers in the current pod
    containers=$(kubectl --kubeconfig "$kubeconfig" get pod "$pod" -n "$wekaclient_namespace" -o jsonpath='{.spec.containers[*].name}')

    # Iterate over each container in the current pod
    for container in $containers; do
      get_pod_logs "$pod" "$container" "$wekaclient_namespace"
    done
  done


  # Iterate over each pod in the CSI namespace
  for pod in $csi_pods; do
    # Describe the pod and place in logs
    describe_pod "$pod" "$csi_namespace"

    # Get all containers in the current pod
    containers=$(kubectl --kubeconfig "$kubeconfig" get pod "$pod" -n "$csi_namespace" -o jsonpath='{.spec.containers[*].name}')

    # Iterate over each container in the current pod
    for container in $containers; do
      get_pod_logs "$pod" "$container" "$csi_namespace"
    done
  done
fi

# Iterate over each node
for node in $nodes; do
  # Describe the node and place in logs
  describe_node "$node"
  # Iterate over each logfile
  for logfile in "${logfile_array[@]}"; do
    get_node_logfile "$node" "$logfile"
  done
done

echo "Logs saved to: $output_dir"

cleanup_debug_pods

RETURN_CODE=0
exit ${RETURN_CODE}

EOF
chmod +x "$WORKDIR/gather_k8s_logs.sh"

# === Write prettyprint.py ===
cat > "$WORKDIR/prettyprint.py" <<'EOF'


import json
import argparse
import re
import os
import sys

parser = argparse.ArgumentParser(description='Process k8s bundle')
parser.add_argument('--config', dest='config_file', default="./config.env", help='Path to the configuration file. Default is ./config.env.')
parser.add_argument('--bundle', dest='bundle_dir', default=".", help='Path to the bundle directory. Default is current directory.')
args = parser.parse_args()

CONFIG_ENV = """
WEKACLUSTER_NAMESPACE="default"
WEKACLIENT_NAMESPACE="default"
OPERATOR_NAMESPACE="weka-operator-system"
CSI_NAMESPACE="csi-wekafs"
"""

# Read from local env config

def read_config_file():
    """Parses embedded config parameters and returns them as a dictionary."""
    config = {}
    for line in CONFIG_ENV.strip().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split("=", 1)
        if len(parts) == 2:
            key = parts[0].strip()
            value = parts[1].strip().strip("'\"")
            config[key] = value
    return config


# Read a configuration file
def read_config_file_old(file_path):
    """Reads a configuration file and returns its content as a Python object."""
    config = {}
    try:
        for line in open(file_path, encoding="utf8"):
            line = line.strip()
            if line:
                # Skip comments
                if line.startswith("#"):
                    continue
                # Create dictionary of key value pairs
                parts = line.split("=")
                if len(parts) == 2:
                    key = parts[0].strip()
                    value = parts[1].strip().strip("'\"")
                    config[key] = value
        return config        
    except FileNotFoundError:
        print(f"ERROR: File not found at {file_path}")
        raise
        

def read_json_file(file_path):
    """Reads a JSON file and returns its content as a Python object."""
    try:
        with open(file_path, 'r') as f:
            data = json.load(f)
            return data
    except FileNotFoundError:
        print(f"WARNING: File not found at "+file_path)
        raise
    except json.JSONDecodeError:
        print(f"ERROR: Invalid JSON format in "+file_path)
        raise
        

def print_pods(pods):
    namespaceWidth=32
    nameWidth = 64
    phaseWidth=16
    hostIPWidth=16
    startTimeWidth=32

    print("{0:<{nsw}} {1:<{nw}} {2:<{pw}} {3:<{hiw}} {4:<{sw}}".format(
        "Namespace", "Name", "Phase", "Host IP", "Start Time", nw=nameWidth, nsw=namespaceWidth, pw=phaseWidth, hiw=hostIPWidth, sw=startTimeWidth))
    print("-" * (nameWidth + namespaceWidth + phaseWidth + hostIPWidth + startTimeWidth))
    for pod in pods:
        metadata = pod.get('metadata', {})
        namespace = metadata.get('namespace')
        name = metadata.get('name')
        phase=pod.get('status',{}).get('phase')
        host_ip=pod.get('status',{}).get('hostIP')
        startTime=pod.get('status',{}).get('startTime')
        print("{0:<{nsw}} {1:<{nw}}  {2:<{pw}} {3:<{hiw}} {4:<{sw}}".format(
            namespace, name, phase, host_ip, startTime, nw=nameWidth, nsw=namespaceWidth, pw=phaseWidth, hiw=hostIPWidth, sw=startTimeWidth))

def print_wekacontainers(wekacontainers):
    namespaceWidth=32
    nameWidth = 64    
    statusWidth=32
    modeWidth=16
    startTimeWidth=32
    
    print("{0:<{nw}} {1:<{nsw}} {2:<{sw}} {3:<{mw}} {4:<{stw}}".format(
        "Name", "Namespace", "Status", "Mode", "Start Time", nw=nameWidth, nsw=namespaceWidth, sw=statusWidth, mw=modeWidth, stw=startTimeWidth))
        
    print("-" * (nameWidth + namespaceWidth + statusWidth + modeWidth + startTimeWidth))

    for wekacontainer in wekacontainers:
        metadata = wekacontainer.get('metadata', {})
        namespace = metadata.get('namespace')
        name = metadata.get('name')
        # Sometimes status of a container is empty (None)
        status=wekacontainer.get('status',{}).get('status') or ""
        # Sometimes mode of a container is empty (None)
        mode=metadata.get('labels',{}).get('weka.io/mode') or ""
        startTime=metadata.get('creationTimestamp')
        print("{0:<{nw}} {1:<{nsw}} {2:<{sw}} {3:<{mw}} {4:<{stw}}".format(
            name, namespace, status, mode, startTime, nw=nameWidth, nsw=namespaceWidth, sw=statusWidth, mw=modeWidth, stw=startTimeWidth))

def print_wekaclients(wekaclients):
    namespaceWidth=32
    nameWidth = 64    
    statusWidth=32
    
    print("{0:<{nsw}} {1:<{nw}} {2:<{sw}} ".format(
        "Namespace", "Name", "Status", nsw=namespaceWidth, nw=nameWidth, sw=statusWidth))
        
    print("-" * (nameWidth + namespaceWidth + statusWidth))

    for wekaclient in wekaclients:
        metadata = wekaclient.get('metadata', {})
        namespace = metadata.get('namespace')
        name = metadata.get('name')
        # Sometimes status of a container is empty (None)
        status=wekaclient.get('status',{}).get('status') or ""
        print("{0:<{nsw}} {1:<{nw}} {2:<{sw}} ".format(
            namespace, name, status, nsw=namespaceWidth, nw=nameWidth, sw=statusWidth))

def print_wekaclusters(wekaclusters):
    namespaceWidth=32
    nameWidth = 64    
    statusWidth=32
    
    print("{0:<{nsw}} {1:<{nw}} {2:<{sw}} ".format(
        "Namespace", "Name", "Status", nsw=namespaceWidth, nw=nameWidth, sw=statusWidth))
        
    print("-" * (nameWidth + namespaceWidth + statusWidth))

    for wekacluster in wekaclusters:
        metadata = wekacluster.get('metadata', {})
        namespace = metadata.get('namespace')
        name = metadata.get('name')
        # Sometimes status of a container is empty (None)
        status=wekacluster.get('status',{}).get('status') or ""
        print("{0:<{nsw}} {1:<{nw}} {2:<{sw}} ".format(
            namespace, name, status, nsw=namespaceWidth, nw=nameWidth, sw=statusWidth))

       
def get_namespaces(config):
     regex_pattern = r"NAMESPACE"
     namespaces = {k: v for k, v in config.items() if re.search(regex_pattern, str(k))}
     return namespaces


def main():
    # Read config file
    if args.config_file:
        config = read_config_file()
        if config:
            print("Configuration loaded successfully from "+args.config_file+".")
        else:
            print("Failed to load configuration.")
    else:
        print("No configuration file provided.")
        return

    # Check bundle directory exists
    if not os.path.exists(args.bundle_dir+"/cluster-info"):
        print("ERROR: Failed to find expected bundle information at "+args.bundle_dir+"/cluster-info.")
        print("  Move bundle or specify --bundle argument.")
        sys.exit(1)

    namespaces = get_namespaces(config)
    print(namespaces)
    
    # For each namespace, print info
    for namespace in namespaces.values():
        pods=[]
        wekacontainers=[]
        wekaclusters=[]
        wekaclients=[]  

        print("")
        print("")
        print("Namespace: " + namespace)
        print("=" * 180)

        print("")
        print("Pods:")
        file_path = args.bundle_dir + '/cluster-info/' + namespace + '/pods.json'
        try:
            data = read_json_file(file_path)
            pods = data.get('items', [])
        except (FileNotFoundError) as err:
            print(file_path+" not found, probably no data for this object type in this namespace, continuing...")

        if pods:
            print_pods(pods)
        else:
            print("No pods found in this namespace.")
            
        print("")
        print("WekaContainers:")
        file_path = args.bundle_dir + '/cluster-info/' + namespace + '/wekacontainer.json'
        try:
            data = read_json_file(file_path)
            wekacontainers = data.get('items', [])
        except (FileNotFoundError) as err:
            print(file_path+" not found, probably no data for this object type in this namespace, continuing...")

        if wekacontainers:
            print_wekacontainers(wekacontainers)
        else:
            print("No WekaContainers found in this namespace.")

        print("")
        print("WekaClusters:")
        file_path = args.bundle_dir + '/cluster-info/' + namespace + '/wekacluster.json'
        try:
            data = read_json_file(file_path)
            wekaclusters = data.get('items', [])
        except (FileNotFoundError) as err:
            print(file_path+" not found, probably no data for this object type in this namespace, continuing...")

        if wekaclusters:
            print_wekaclusters(wekaclusters)
        else:
            print("No WekaClusters found in this namespace.")
        
        print("")
        print("WekaClients:")
        file_path = args.bundle_dir + '/cluster-info/' + namespace + '/wekaclient.json'
        try:
            data = read_json_file(file_path)
            wekaclients = data.get('items', [])
        except (FileNotFoundError) as err:
            print(file_path+" not found, probably no data for this object type in this namespace, continuing...")
            
        if wekaclients:
            print_wekaclients(wekaclients)
        else:
            print("No WekaClients found in this namespace.")
        
    """
    Print pods
    file_path = 'logs/dev/cluster-info/weka-operator-system/pods.json'
    data = read_json_file(file_path)
    pods = data.get('items', [])
    print_pods(pods)
    """

"""
    for pod in pods:
        metadata = pod.get('metadata', {})
        namespace = metadata.get('namespace')
        name = metadata.get('name')
        phase=pod.get('status',{}).get('phase')
        host_ip=pod.get('status',{}).get('hostIP')
        startTime=pod.get('status',{}).get('startTime')
"""     

if __name__ == "__main__":
    main()  

EOF

## "$WORKDIR/gather_k8s_logs.sh"
## python3 "$WORKDIR/prettyprint.py" --config "$WORKDIR/config.env" --bundle logs

DESCRIPTION="Script to gather K8S weka cluster logs"
# script type is single, parallel, sequential, or parallel-compare-backends
SCRIPT_TYPE="single"
RETURN_CODE=0

# SPECIFICS
logs_collect="$WORKDIR/gather_k8s_logs.sh"
pretty_print="$WORKDIR/prettyprint.py"

function check_if_in_k8s() {

# If kubectl not found, meaning we are either inside a POD or on a system where no kubectl is running
which kubectl
if [ $? -eq 1 ]; then
	echo "kubectl doesn't exist"
	RETURN_CODE=1
	exit ${RETURN_CODE}
else
	RETURN_CODE=0
fi

}

function call_gather_logs () {

if [ ! -f $logs_collect ]; then
	echo "could not find $logs_collect script"
	RETURN_CODE=1
	exit ${RETURN_CODE}
else
	rm -rf logs
	$logs_collect
	log_dir=$(find "logs" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\0' |sort -znr | sed -zE 's/^[^ ]+ //;q')
	if [ ! -f $pretty_print ]; then
		echo "Could not find $pretty_print script"
			RETURN_CODE=1
			exit ${RETURN_CODE}
	else
		python3 $pretty_print --bundle $log_dir
		RETURN_CODE=0
	fi
fi

}


### MAIN ###
check_if_in_k8s
call_gather_logs

RETURN_CODE=0
exit ${RETURN_CODE}
