#!/bin/bash

DESCRIPTION="Collect log files from /var/log and /opt/weka/logs + dmesg -T"
SCRIPT_TYPE="parallel"

# Collect log files from /var/log and /opt/weka/logs + dmesg -T
ret=0
internalip=`ip ro li | grep src | tail -1 | awk {'print $9'}`
ping -i 1 -c 2 8.8.8.8 1> /dev/null 2> /dev/null
if [ $? -eq 0 ]; then
	externalip=`curl -s http://checkip.amazonaws.com`
else
	externalip=""
fi
name=`hostname`
timestamp=$(date +%Y%m%d_%H%M%S)


output_weka_logs="weka_diags_"$name"_"$internalip"_"$externalip"_"$timestamp".txt"
output_system_logs="system_diags_"$name"_"$internalip"_"$externalip"_"$timestamp".txt"
output_host_logs="host_diags_"$name"_"$internalip"_"$externalip"_"$timestamp".txt"

workdir="/tmp/diagnostics_"$name"_"$internalip"_"$externalip"_"$timestamp""
mkdir -p "$workdir"

function analyze() {
local folder="$1"
local pattern="ERROR|FAIL|ASSERT"
local max_repeats=3

if [ -z "$folder" ]; then
	echo "Usage: analyze <path-to-folder>"
	return 1
fi

if [ ! -d "$folder" ]; then
	echo "Error: '$folder' is not a directory."
	return 2
fi

find "$folder" -type f | while read -r file; do
matches=$(grep -iE "$pattern" "$file" 2>/dev/null | grep -iv "DEBUG")

if [ -n "$matches" ]; then
	echo -e "\n==> $(realpath "$file")"
	echo "$matches" | awk -v max="$max_repeats" '
	{
		original_line = $0

    		# Normalize:
    		line = $0
    		# Remove timestamp + host (as before)
    		gsub(/^[A-Z][a-z]{2} [ 0-9][0-9] [0-9]{2}:[0-9]{2}:[0-9]{2} [^ ]+ /, "", line)
    		gsub(/^[0-9]{4}\/[0-9]{2}\/[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2} /, "", line)  # nginx-style timestamp
    		# Remove nginx connection IDs like *30
    		gsub(/\*[0-9]+/, "*", line)

    		count[line]++
    		if (count[line] <= max) {
      			print original_line
    			} else if (count[line] == max + 1) {
      				print "... repeated more than " max " times: " substr(line, 1, 80) " ..."
    				}
  	}
	'| tail -n 100
fi
done

}

analyze /var/log > $workdir/$output_system_logs
analyze /opt/weka/logs > $workdir/$output_weka_logs

#cat $workdir/$output_system_logs
#cat $workdir/$output_weka_logs

dmesg -T > $workdir/dmesg_output.txt

echo "Diagnostics collected in: $workdir"

exit $ret


