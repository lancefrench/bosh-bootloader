package terraform_test

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/cloudfoundry/bosh-bootloader/fakes"
	"github.com/cloudfoundry/bosh-bootloader/storage"
	"github.com/cloudfoundry/bosh-bootloader/terraform"
	"github.com/pivotal-cf-experimental/gomegamatchers"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Manager", func() {
	var (
		executor              *fakes.TerraformExecutor
		templateGenerator     *fakes.TemplateGenerator
		inputGenerator        *fakes.InputGenerator
		outputGenerator       *fakes.OutputGenerator
		logger                *fakes.Logger
		manager               terraform.Manager
		terraformOutputBuffer bytes.Buffer
		expectedTFState       string
		expectedTFOutput      string
	)

	BeforeEach(func() {
		executor = &fakes.TerraformExecutor{}
		templateGenerator = &fakes.TemplateGenerator{}
		inputGenerator = &fakes.InputGenerator{}
		outputGenerator = &fakes.OutputGenerator{}
		logger = &fakes.Logger{}

		expectedTFOutput = "some terraform output"
		expectedTFState = "some-updated-tf-state"

		manager = terraform.NewManager(terraform.NewManagerArgs{
			Executor:              executor,
			TemplateGenerator:     templateGenerator,
			InputGenerator:        inputGenerator,
			OutputGenerator:       outputGenerator,
			TerraformOutputBuffer: &terraformOutputBuffer,
			Logger:                logger,
		})
	})

	Describe("Apply", func() {
		var (
			incomingState storage.State
			expectedState storage.State
		)

		BeforeEach(func() {
			incomingState = storage.State{
				IAAS:  "gcp",
				EnvID: "some-env-id",
				GCP: storage.GCP{
					ServiceAccountKey: "some-service-account-key",
					ProjectID:         "some-project-id",
					Zone:              "some-zone",
					Region:            "some-region",
				},
				TFState: "some-tf-state",
				LB: storage.LB{
					Type:   "cf",
					Domain: "some-domain",
				},
			}

			executor.ApplyCall.Returns.TFState = expectedTFState

			expectedState = incomingState
			expectedState.TFState = expectedTFState
			expectedState.LatestTFOutput = expectedTFOutput

			templateGenerator.GenerateCall.Returns.Template = "some-gcp-terraform-template"
			inputGenerator.GenerateCall.Returns.Inputs = map[string]string{
				"env_id":        incomingState.EnvID,
				"project_id":    incomingState.GCP.ProjectID,
				"region":        incomingState.GCP.Region,
				"zone":          incomingState.GCP.Zone,
				"credentials":   "some-path",
				"system_domain": incomingState.LB.Domain,
			}
		})

		It("logs steps", func() {
			_, err := manager.Apply(storage.State{})
			Expect(err).NotTo(HaveOccurred())

			Expect(logger.StepCall.Messages).To(gomegamatchers.ContainSequence([]string{
				"generating terraform template",
				"generating terraform variables",
				"applying terraform template",
			}))
		})

		Context("when the iaas is aws", func() {
			var awsState storage.State
			BeforeEach(func() {
				awsState = storage.State{
					IAAS:    "aws",
					EnvID:   "some-env-id",
					TFState: "some-tf-state",
				}
				inputGenerator.GenerateCall.Returns.Inputs = map[string]string{
					"env_id": incomingState.EnvID,
				}
				templateGenerator.GenerateCall.Returns.Template = "some-terraform-template"
			})

			It("logs steps", func() {
				_, err := manager.Apply(storage.State{IAAS: "aws"})
				Expect(err).NotTo(HaveOccurred())

				Expect(logger.StepCall.Messages).To(gomegamatchers.ContainSequence([]string{
					"generating terraform template",
					"generating terraform variables",
					"applying terraform template",
				}))
			})

			It("returns a state with new tfState and output from executor apply", func() {
				terraformOutputBuffer.Write([]byte("some-updated-tf-state"))

				expectedAWSState := awsState
				expectedAWSState.TFState = "some-updated-tf-state"
				expectedAWSState.LatestTFOutput = "some-updated-tf-state"
				state, err := manager.Apply(awsState)
				Expect(err).NotTo(HaveOccurred())

				Expect(templateGenerator.GenerateCall.Receives.State).To(Equal(awsState))
				Expect(inputGenerator.GenerateCall.Receives.State).To(Equal(awsState))

				Expect(executor.ApplyCall.Receives.Inputs).To(HaveKeyWithValue("env_id", awsState.EnvID))
				Expect(executor.ApplyCall.Receives.TFState).To(Equal("some-tf-state"))
				Expect(executor.ApplyCall.Receives.Template).To(Equal(string("some-terraform-template")))
				Expect(state).To(Equal(expectedAWSState))
			})
		})

		It("returns a state with new tfState and output from executor apply", func() {
			terraformOutputBuffer.Write([]byte(expectedTFOutput))

			state, err := manager.Apply(incomingState)
			Expect(err).NotTo(HaveOccurred())

			Expect(templateGenerator.GenerateCall.Receives.State).To(Equal(incomingState))
			Expect(inputGenerator.GenerateCall.Receives.State).To(Equal(incomingState))

			Expect(executor.ApplyCall.Receives.Inputs).To(Equal(map[string]string{
				"env_id":        incomingState.EnvID,
				"project_id":    incomingState.GCP.ProjectID,
				"region":        incomingState.GCP.Region,
				"zone":          incomingState.GCP.Zone,
				"credentials":   "some-path",
				"system_domain": incomingState.LB.Domain,
			}))
			Expect(executor.ApplyCall.Receives.TFState).To(Equal("some-tf-state"))
			Expect(executor.ApplyCall.Receives.Template).To(Equal(string("some-gcp-terraform-template")))
			Expect(state).To(Equal(expectedState))
		})

		Context("when an error occurs", func() {
			Context("when InputGenerator.Generate returns an error", func() {
				BeforeEach(func() {
					inputGenerator.GenerateCall.Returns.Error = errors.New("failed to generate inputs")
				})

				It("bubbles up the error", func() {
					_, err := manager.Apply(incomingState)
					Expect(err).To(MatchError("failed to generate inputs"))
				})
			})

			Context("when the applying causes an executor error", func() {
				BeforeEach(func() {
					executor.ApplyCall.Returns.Error = &fakes.TerraformExecutorError{}

					terraformOutputBuffer.Write([]byte(expectedTFOutput))
				})

				AfterEach(func() {
					executor.ApplyCall.Returns.Error = nil
				})

				It("returns the bblState with latest terraform output and a ManagerError", func() {
					_, err := manager.Apply(incomingState)

					Expect(err).To(BeAssignableToTypeOf(terraform.ManagerError{}))
				})
			})

			Context("when Executor.Apply returns a non-ExecutorError error", func() {
				executorError := errors.New("some-error")

				BeforeEach(func() {
					executor.ApplyCall.Returns.Error = executorError
				})

				AfterEach(func() {
					executor.ApplyCall.Returns.Error = nil
				})

				It("bubbles up the error", func() {
					_, err := manager.Apply(incomingState)
					Expect(err).To(Equal(executorError))
				})
			})
		})
	})

	Describe("Destroy", func() {
		Context("when the bbl state contains a non-empty TFState", func() {
			var (
				incomingState storage.State
				expectedState storage.State
			)

			BeforeEach(func() {
				incomingState = storage.State{
					EnvID: "some-env-id",
					GCP: storage.GCP{
						ServiceAccountKey: "some-service-account-key",
						ProjectID:         "some-project-id",
						Zone:              "some-zone",
						Region:            "some-region",
					},
					LB: storage.LB{
						Type:   "cf",
						Domain: "some-domain",
					},
					TFState: "some-tf-state",
				}
				executor.DestroyCall.Returns.TFState = expectedTFState

				expectedState = incomingState
				expectedState.TFState = expectedTFState
				expectedState.LatestTFOutput = expectedTFOutput

				templateGenerator.GenerateCall.Returns.Template = "some-gcp-terraform-template"
				inputGenerator.GenerateCall.Returns.Inputs = map[string]string{
					"env_id":        incomingState.EnvID,
					"project_id":    incomingState.GCP.ProjectID,
					"region":        incomingState.GCP.Region,
					"zone":          incomingState.GCP.Zone,
					"credentials":   "some-path",
					"system_domain": incomingState.LB.Domain,
				}
			})

			It("logs steps", func() {
				_, err := manager.Destroy(incomingState)
				Expect(err).NotTo(HaveOccurred())

				Expect(logger.StepCall.Messages).To(gomegamatchers.ContainSequence([]string{
					"destroying infrastructure", "finished destroying infrastructure",
				}))
			})

			It("calls Executor.Destroy with the right arguments", func() {
				_, err := manager.Destroy(incomingState)
				Expect(err).NotTo(HaveOccurred())

				Expect(templateGenerator.GenerateCall.Receives.State).To(Equal(incomingState))

				Expect(inputGenerator.GenerateCall.Receives.State).To(Equal(incomingState))

				Expect(executor.DestroyCall.Receives.Inputs).To(Equal(map[string]string{
					"env_id":        incomingState.EnvID,
					"project_id":    incomingState.GCP.ProjectID,
					"region":        incomingState.GCP.Region,
					"zone":          incomingState.GCP.Zone,
					"credentials":   "some-path",
					"system_domain": incomingState.LB.Domain,
				}))
				Expect(executor.DestroyCall.Receives.Template).To(Equal(templateGenerator.GenerateCall.Returns.Template))
				Expect(executor.DestroyCall.Receives.TFState).To(Equal(incomingState.TFState))
			})

			It("returns the bbl state updated with the TFState and output from executor destroy", func() {
				terraformOutputBuffer.Write([]byte(expectedTFOutput))

				newBBLState, err := manager.Destroy(incomingState)
				Expect(err).NotTo(HaveOccurred())

				Expect(newBBLState).To(Equal(expectedState))
			})

			Context("when InputGenerator.Generate returns an error", func() {
				BeforeEach(func() {
					inputGenerator.GenerateCall.Returns.Error = errors.New("failed to generate inputs")
				})

				It("bubbles up the error", func() {
					_, err := manager.Apply(incomingState)
					Expect(err).To(MatchError("failed to generate inputs"))
				})
			})

			Context("when Executor.Destroy returns a ExecutorError", func() {
				var (
					tempDir       string
					executorError *fakes.TerraformExecutorError
				)

				BeforeEach(func() {
					var err error
					tempDir, err = ioutil.TempDir("", "")
					Expect(err).NotTo(HaveOccurred())

					err = ioutil.WriteFile(filepath.Join(tempDir, "terraform.tfstate"), []byte("updated-tf-state"), os.ModePerm)
					Expect(err).NotTo(HaveOccurred())

					executorError = &fakes.TerraformExecutorError{}
					executor.DestroyCall.Returns.Error = executorError

					terraformOutputBuffer.Write([]byte(expectedTFOutput))
				})

				AfterEach(func() {
					executor.DestroyCall.Returns.Error = nil
				})

				It("returns a ManagerError", func() {
					_, err := manager.Destroy(incomingState)

					expectedState := incomingState
					expectedState.LatestTFOutput = expectedTFOutput
					expectedError := terraform.NewManagerError(expectedState, executorError)
					Expect(err).To(MatchError(expectedError))
				})
			})

			Context("when Executor.Destroy returns a non-ExecutorError error", func() {
				executorError := errors.New("some-error")

				BeforeEach(func() {
					executor.DestroyCall.Returns.Error = executorError
				})

				AfterEach(func() {
					executor.DestroyCall.Returns.Error = nil
				})

				It("bubbles up the error", func() {
					_, err := manager.Destroy(incomingState)
					Expect(err).To(Equal(executorError))
				})
			})
		})
		Context("when the bbl state contains a non-empty TFState", func() {
			var (
				incomingState = storage.State{EnvID: "some-env-id"}
			)
			It("returns the bbl state and skips calling executor destroy", func() {
				bblState, err := manager.Destroy(incomingState)
				Expect(err).NotTo(HaveOccurred())

				Expect(bblState).To(Equal(incomingState))
				Expect(executor.DestroyCall.CallCount).To(Equal(0))
			})
		})
	})

	Describe("GetOutputs", func() {
		BeforeEach(func() {
			outputGenerator.GenerateCall.Returns.Outputs = map[string]interface{}{
				"external_ip": "some-external-ip",
			}
		})

		It("returns all terraform outputs except lb related outputs", func() {
			incomingState := storage.State{
				IAAS:    "gcp",
				TFState: "some-tf-state",
			}

			terraformOutputs, err := manager.GetOutputs(incomingState)
			Expect(err).NotTo(HaveOccurred())

			Expect(outputGenerator.GenerateCall.Receives.TFState).To(Equal("some-tf-state"))
			Expect(terraformOutputs).To(Equal(map[string]interface{}{
				"external_ip": "some-external-ip",
			}))
		})

		Context("when the output generator fails", func() {
			It("returns the error to the caller", func() {
				outputGenerator.GenerateCall.Returns.Error = errors.New("fail")
				_, err := manager.GetOutputs(storage.State{
					IAAS: "gcp",
				})
				Expect(err).To(MatchError("fail"))
			})
		})
	})

	Describe("Version", func() {
		BeforeEach(func() {
			executor.VersionCall.Returns.Version = "some-version"
		})

		It("returns a version", func() {
			version, err := manager.Version()
			Expect(err).NotTo(HaveOccurred())

			Expect(executor.VersionCall.CallCount).To(Equal(1))
			Expect(version).To(Equal("some-version"))
		})

		Context("when executor version returns an error", func() {
			BeforeEach(func() {
				executor.VersionCall.Returns.Error = errors.New("failed to get version")
			})

			It("returns the error", func() {
				_, err := manager.Version()
				Expect(err).To(MatchError("failed to get version"))
			})
		})
	})

	Describe("ValidateVersion", func() {
		Context("when terraform version is greater than the minimum", func() {
			BeforeEach(func() {
				executor.VersionCall.Returns.Version = "9.0.0"
			})

			It("validates the version of terraform and returns no error", func() {
				err := manager.ValidateVersion()
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("failure cases", func() {
			Context("when terraform version is less than the minimum", func() {
				It("returns an error", func() {
					executor.VersionCall.Returns.Version = "0.0.1"

					err := manager.ValidateVersion()
					Expect(err).To(MatchError("Terraform version must be at least v0.10.0"))
				})
			})

			Context("when terraform executor fails to get the version", func() {
				It("fast fails", func() {
					executor.VersionCall.Returns.Error = errors.New("cannot get version")

					err := manager.ValidateVersion()
					Expect(err).To(MatchError("cannot get version"))
				})
			})

			Context("when terraform version cannot be parsed by go-semver", func() {
				It("fast fails", func() {
					executor.VersionCall.Returns.Version = "lol.5.2"

					err := manager.ValidateVersion()
					Expect(err.Error()).To(ContainSubstring("invalid syntax"))
				})
			})
		})
	})
})
