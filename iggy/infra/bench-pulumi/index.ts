import * as pulumi from "@pulumi/pulumi";
import * as aws from "@pulumi/aws";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";

// Two-VM benchmark topology in us-east-1a:
//   VM1 producer (c7i.2xlarge)        -> isolated load generator
//   VM2 broker + connectors (c6id.4xlarge, local NVMe) -> the thing under test
// Both feed a managed VeloDB Cloud cluster (provisioned outside Pulumi).
// Same subnet/AZ keeps inter-VM latency low and consistent.

const cfg = new pulumi.Config();
const region = "us-east-1";
const az = `${region}a`;
// Restrict SSH. Override with: pulumi config set sshCidr <your-ip>/32
const sshCidr = cfg.get("sshCidr") ?? "0.0.0.0/0";
const sshPubKeyPath = cfg.get("sshPublicKeyPath") ?? path.join(os.homedir(), ".ssh/id_ed25519.pub");

const provider = new aws.Provider("aws", { region });
const opts = { provider };
const tags = { Project: "iggy-bench" };

// --- Network ---------------------------------------------------------------
const vpc = new aws.ec2.Vpc("bench-vpc", {
    cidrBlock: "10.0.0.0/16",
    enableDnsHostnames: true,
    enableDnsSupport: true,
    tags: { ...tags, Name: "iggy-bench-vpc" },
}, opts);

const igw = new aws.ec2.InternetGateway("bench-igw", {
    vpcId: vpc.id,
    tags: { ...tags, Name: "iggy-bench-igw" },
}, opts);

const subnet = new aws.ec2.Subnet("bench-subnet", {
    vpcId: vpc.id,
    cidrBlock: "10.0.1.0/24",
    availabilityZone: az,
    mapPublicIpOnLaunch: true,
    tags: { ...tags, Name: "iggy-bench-subnet" },
}, opts);

const routeTable = new aws.ec2.RouteTable("bench-rt", {
    vpcId: vpc.id,
    routes: [{ cidrBlock: "0.0.0.0/0", gatewayId: igw.id }],
    tags: { ...tags, Name: "iggy-bench-rt" },
}, opts);

new aws.ec2.RouteTableAssociation("bench-rta", {
    subnetId: subnet.id,
    routeTableId: routeTable.id,
}, opts);

// --- Security group: SSH in, all traffic between the two VMs ----------------
const sg = new aws.ec2.SecurityGroup("bench-sg", {
    vpcId: vpc.id,
    description: "iggy-bench: SSH ingress, all intra-SG traffic",
    ingress: [{
        protocol: "tcp", fromPort: 22, toPort: 22,
        cidrBlocks: [sshCidr], description: "SSH",
    }],
    egress: [{ protocol: "-1", fromPort: 0, toPort: 0, cidrBlocks: ["0.0.0.0/0"] }],
    tags: { ...tags, Name: "iggy-bench-sg" },
}, opts);

// Producer <-> broker on any port (Iggy 8090, Kafka 9092, etc.) without
// enumerating ports.
new aws.ec2.SecurityGroupRule("bench-sg-self", {
    type: "ingress",
    securityGroupId: sg.id,
    sourceSecurityGroupId: sg.id,
    protocol: "-1", fromPort: 0, toPort: 0,
    description: "intra-SG all",
}, opts);

// --- Key pair (uses your existing local public key) ------------------------
const keyPair = new aws.ec2.KeyPair("bench-key", {
    keyNamePrefix: "iggy-bench-",
    publicKey: fs.readFileSync(sshPubKeyPath, "utf8").trim(),
    tags,
}, opts);

// --- AMI: Amazon Linux 2023 x86_64 (kernel 6.x, so io_uring >= 5.19) --------
// Looked up via DescribeImages (the us-demo-sa user lacks ssm:GetParameter for
// the public AMI alias).
const amiId = aws.ec2.getAmiOutput({
    owners: ["amazon"],
    mostRecent: true,
    filters: [
        { name: "name", values: ["al2023-ami-2023.*-x86_64"] },
        { name: "architecture", values: ["x86_64"] },
        { name: "state", values: ["available"] },
        { name: "virtualization-type", values: ["hvm"] },
    ],
}, opts).id;

// --- user-data: composed from the versioned scripts in ./scripts ------------
// Single source of truth — the same files you can scp + re-run on a live VM.
const scriptsDir = path.join(__dirname, "scripts");
const readScript = (name: string) => fs.readFileSync(path.join(scriptsDir, name), "utf8");
const stripShebang = (s: string) => s.replace(/^#![^\n]*\n/, "");

const commonScript = readScript("common-setup.sh");
const brokerScript = readScript("broker-setup.sh");

const producerUserData = commonScript;
// Broker runs common setup, then the NVMe mount (shebang stripped on the append).
const brokerUserData = `${commonScript}\n${stripShebang(brokerScript)}`;

// --- Instances -------------------------------------------------------------
function makeInstance(name: string, instanceType: string, userData: string) {
    return new aws.ec2.Instance(name, {
        ami: amiId,
        instanceType,
        subnetId: subnet.id,
        vpcSecurityGroupIds: [sg.id],
        keyName: keyPair.keyName,
        availabilityZone: az,
        rootBlockDevice: {
            volumeType: "gp3",
            volumeSize: 100,
            iops: 3000,
            throughput: 250,
        },
        userData,
        userDataReplaceOnChange: true,
        tags: { ...tags, Name: name },
    }, opts);
}

const producer = makeInstance("iggy-bench-producer", "c7i.2xlarge", producerUserData);
const broker = makeInstance("iggy-bench-broker", "c6id.4xlarge", brokerUserData);

// --- Outputs ---------------------------------------------------------------
export const producerPublicIp = producer.publicIp;
export const producerPrivateIp = producer.privateIp;
export const brokerPublicIp = broker.publicIp;
export const brokerPrivateIp = broker.privateIp;
export const sshProducer = pulumi.interpolate`ssh ec2-user@${producer.publicIp}`;
export const sshBroker = pulumi.interpolate`ssh ec2-user@${broker.publicIp}`;
// Point the producer at the broker over the private network:
export const brokerIggyEndpoint = pulumi.interpolate`iggy://iggy:iggy@${broker.privateIp}:8090`;
export const brokerKafkaEndpoint = pulumi.interpolate`${broker.privateIp}:9092`;
