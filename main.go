package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/sns"
)

type LifecycleHookMsg struct {
	Ec2InstanceId        string
	LifecycleHookName    string
	LifecycleTransition  string
	AsgGroupName         string
	TopicArn             string
	Message              string
	NotificationMetadata NotificationMetadata
}

type NotificationMetadata struct {
	ClusterName string
}

const (
	TerminatingTransition = "autoscaling:EC2_INSTANCE_TERMINATING"
)

func parseEvent(eventStr string) (*LifecycleHookMsg, error) {
	var iEvent interface{}
	if err := json.Unmarshal([]byte(eventStr), &iEvent); err != nil {
		return nil, err
	}
	record := iEvent.(map[string]interface{})["Records"].([]interface{})[0]
	snsMap := record.(map[string]interface{})["Sns"]
	topicArn := snsMap.(map[string]interface{})["TopicArn"].(string)
	msgStr := snsMap.(map[string]interface{})["Message"].(string)

	var iMessage interface{}

	if err := json.Unmarshal([]byte(msgStr), &iMessage); err != nil {
		return nil, err
	}

	lifecycleHookMsgMap := iMessage.(map[string]interface{})
	metadataStr := lifecycleHookMsgMap["NotificationMetadata"].(string)
	var metadata NotificationMetadata
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		return nil, err
	}

	lifecycleHookMsg := LifecycleHookMsg{
		Ec2InstanceId:        lifecycleHookMsgMap["EC2InstanceId"].(string),
		LifecycleTransition:  lifecycleHookMsgMap["LifecycleTransition"].(string),
		LifecycleHookName:    lifecycleHookMsgMap["LifecycleHookName"].(string),
		AsgGroupName:         lifecycleHookMsgMap["AutoScalingGroupName"].(string),
		NotificationMetadata: metadata,
		TopicArn:             topicArn,
		Message:              msgStr,
	}
	return &lifecycleHookMsg, nil
}

func getContainerInstance(ecsClient *ecs.ECS, clusterName string, ec2InstanceId string) (*ecs.ContainerInstance, error) {
	lciParams := &ecs.ListContainerInstancesInput{
		Cluster: aws.String(clusterName),
	}
	lciResp, err := ecsClient.ListContainerInstances(lciParams)
	if err != nil {
		return nil, err
	}

	dciParams := &ecs.DescribeContainerInstancesInput{
		ContainerInstances: lciResp.ContainerInstanceArns,
		Cluster:            aws.String(clusterName),
	}
	dciResp, err := ecsClient.DescribeContainerInstances(dciParams)
	if err != nil {
		return nil, err
	}
	for _, v := range dciResp.ContainerInstances {
		if *v.Ec2InstanceId == ec2InstanceId {
			return v, nil
		}
	}
	return nil, errors.New("Error: container instance " + ec2InstanceId + " is not exist.")
}

func drainContainerInstance(ecsClient *ecs.ECS, clusterName string, containerInstanceArn string) error {
	ucisParams := &ecs.UpdateContainerInstancesStateInput{
		ContainerInstances: []*string{aws.String(containerInstanceArn)},
		Status:             aws.String("DRAINING"),
		Cluster:            aws.String(clusterName),
	}
	_, err := ecsClient.UpdateContainerInstancesState(ucisParams)
	return err
}

func checkRunningTasks(ecsClient *ecs.ECS, clusterName string, containerInstanceArn string) (bool, error) {
	var taskRunning bool
	if containerInstanceArn != "" {
		ltParams := &ecs.ListTasksInput{
			Cluster:           aws.String(clusterName),
			ContainerInstance: aws.String(containerInstanceArn),
		}
		ltResp, err := ecsClient.ListTasks(ltParams)
		if err != nil {
			return false, err
		}

		runningTaskCount := 0
		if len(ltResp.TaskArns) > 0 {
			dtParams := &ecs.DescribeTasksInput{
				Tasks:   ltResp.TaskArns,
				Cluster: aws.String(clusterName),
			}
			dtResp, err := ecsClient.DescribeTasks(dtParams)
			if err != nil {
				return false, err
			}

			for _, v := range dtResp.Tasks {
				if !strings.HasPrefix(*v.Group, "service:") {
					stParams := &ecs.StopTaskInput{
						Cluster: aws.String(clusterName),
						Reason:  aws.String("Drain container instance"),
						Task:    v.TaskArn,
					}
					_, err := ecsClient.StopTask(stParams)

					if err != nil {
						return false, err
					}
				} else {
					runningTaskCount += 1
				}
			}
		}

		fmt.Printf("Running task count: %d\n", runningTaskCount)
		if runningTaskCount > 0 {
			taskRunning = true
		} else {
			taskRunning = false
		}
	} else {
		taskRunning = false
	}
	return taskRunning, nil
}

func publishSnsMessage(sess *session.Session, lifecycleHookMsg *LifecycleHookMsg) error {
	snsClient := sns.New(sess)
	snsParams := &sns.PublishInput{
		TopicArn: aws.String(lifecycleHookMsg.TopicArn),
		Message:  aws.String(lifecycleHookMsg.Message),
		Subject:  aws.String("Publishing SNS message to invoke lambda again.."),
	}
	_, err := snsClient.Publish(snsParams)
	return err
}

func completeLifecycleAction(sess *session.Session, lifecycleHookMsg *LifecycleHookMsg) error {
	asgClient := autoscaling.New(sess)
	asgParams := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  aws.String(lifecycleHookMsg.AsgGroupName),
		LifecycleHookName:     aws.String(lifecycleHookMsg.LifecycleHookName),
		InstanceId:            aws.String(lifecycleHookMsg.Ec2InstanceId),
		LifecycleActionResult: aws.String("CONTINUE"),
	}
	_, err := asgClient.CompleteLifecycleAction(asgParams)
	return err
}

func main() {
	eventStr := os.Args[1]
	lifecycleHookMsg, err := parseEvent(eventStr)
	if err != nil {
		panic(err)
	}
	sess := session.Must(session.NewSession())

	if lifecycleHookMsg.LifecycleTransition != "" {
		if lifecycleHookMsg.LifecycleTransition == TerminatingTransition {
			ecsClient := ecs.New(sess)
			clusterName := lifecycleHookMsg.NotificationMetadata.ClusterName
			ec2InstanceId := lifecycleHookMsg.Ec2InstanceId

			fmt.Println("Check container instance status...")
			containerInstance, err := getContainerInstance(ecsClient, clusterName, ec2InstanceId)
			if err != nil {
				panic(err)
			}
			containerInstanceArn := *containerInstance.ContainerInstanceArn
			status := *containerInstance.Status
			fmt.Printf("Container instance %s is %s.\n", ec2InstanceId, status)

			if status != "DRAINING" {
				fmt.Println("Draining container instance...")
				err := drainContainerInstance(ecsClient, clusterName, containerInstanceArn)
				if err != nil {
					panic(err)
				}
			}
			fmt.Println("Checking running tasks on container instance.")
			taskRunning, err := checkRunningTasks(ecsClient, clusterName, containerInstanceArn)
			if err != nil {
				panic(err)
			}
			if taskRunning {
				time.Sleep(1 * time.Second)
				fmt.Println("Tasks is running still.")
				if err := publishSnsMessage(sess, lifecycleHookMsg); err != nil {
					panic(err)
				}
				fmt.Println("Rerun check running tasks.")
			} else {
				fmt.Println("Tasks is not exist on container instace.")
				if err := completeLifecycleAction(sess, lifecycleHookMsg); err != nil {
					panic(err)
				}
				fmt.Println("Complete lifecycle action.")
			}
		}
	}
}
