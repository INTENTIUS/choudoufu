// Trimmed excerpt of former2's js/services/ec2.js (iann0036/former2, commit
// 7d354df27db5a8260950021b2273758ba5df9f62), kept only for
// former2_test.go's TestExtractFormer2RowsAgainstExcerpt: enough real
// tracked_resources.push({...}) shapes to exercise extractFormer2Rows
// against actual former2 source rather than a hand-built fixture, plus one
// push with no 'type' field (a real shape former2 uses for a resource it
// tracks without a CloudFormation type of its own) to exercise the
// skip-when-missing path.

if (obj.type == "ec2.instance") {
    tracked_resources.push({
        'obj': obj,
        'logicalId': getResourceName('ec2', obj.id, 'AWS::EC2::Instance'),
        'region': obj.region,
        'service': 'ec2',
        'type': 'AWS::EC2::Instance',
        'terraformType': 'aws_instance',
        'options': reqParams,
        'returnValues': {
            'Ref': obj.data.InstanceId
        }
    });
} else if (obj.type == "ec2.placementgroup") {
    tracked_resources.push({
        'obj': obj,
        'logicalId': getResourceName('ec2', obj.id, 'AWS::EC2::PlacementGroup'),
        'region': obj.region,
        'service': 'ec2',
        'type': 'AWS::EC2::PlacementGroup',
        'terraformType': 'aws_placement_group',
        'options': reqParams,
        'returnValues': {
            'Ref': obj.data.GroupName
        }
    });
} else if (obj.type == "ec2.ebsvolume") {
    tracked_resources.push({
        'obj': obj,
        'logicalId': getResourceName('ec2', obj.id, 'AWS::EC2::Volume'),
        'region': obj.region,
        'service': 'ec2',
        'type': 'AWS::EC2::Volume',
        'terraformType': 'aws_ebs_volume',
        'options': reqParams
    });
} else if (obj.type == "ec2.dxconnection") {
    // A real former2 shape (js/services/directconnect.js): tracked for
    // Terraform output with no 'type' field at all in this literal -
    // extractFormer2Rows must skip it, not invent a pairing.
    tracked_resources.push({
        'obj': obj,
        'logicalId': getResourceName('directconnect', obj.id, 'DXConnection'),
        'region': obj.region,
        'service': 'directconnect',
        'terraformType': 'aws_dx_connection',
        'options': reqParams
    });
}
