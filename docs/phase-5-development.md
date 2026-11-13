# Phase 5 catalog and sale publication

Apply the transactional publication migration and start the four replicas and three Phase 5 services.

```sh
make dev-up migrate
export FLASHKART_ADMIN_TOKEN="$(openssl rand -hex 32)"
make run-market
```

| Service | Local address |
| --- | --- |
| Product Catalog HTTP | `127.0.0.1:8201` |
| Flash Sale Manager HTTP | `127.0.0.1:8202` |
| Inventory HTTP health | `127.0.0.1:8203` |
| Inventory gRPC | `127.0.0.1:7101` |

Product and sale browsing is public. Send the configured bearer token for product changes, conflict resolution, replica inspection, and draft/publication operations. Keep the token in your shell or secret manager. The local launcher passes the separate database roles to Flash Sale Manager and Inventory.

1. Create a product with `PUT /api/admin/products/{product_id}`. Supply an operation ID, the product, and an empty causal context for its first write. For edits, supply the context from the product detail response.
2. Create and edit a draft through `/api/admin/sale-drafts`. The draft update accepts the full set of terms.
3. Publish with `POST /api/admin/sale-drafts/{sale_id}/publish` and a stable operation ID. Poll `GET /api/admin/publications/{operation_id}` until it reports `PUBLISHED`.
4. Retrieve the replicated sale through Product Catalog at `GET /api/sales/{sale_id}`. The workflow can expose the replicated sale before the final inventory activation checkpoint. Use publication status to confirm activation.

Product details and listing return `CATALOG_CONFLICT` with the sibling values and context when a product has concurrent versions. Submit a chosen replacement with that context to `POST /api/admin/products/{product_id}/resolve`. A later concurrent update can produce a new conflict.

The publication worker resumes from a SQL checkpoint after preparing Inventory, journaling the replica envelope, applying it, and activating Inventory. A retry keeps the original operation and preparation origin. A first product write also uses a stable origin derived from its operation ID. If that specific origin is unavailable, the client must retry the same operation when it returns. Reads and prepared writes can use another coordinator.

## Images

Products can reference HTTPS images or an object in the configured S3 image bucket. Upload the image through S3 and supply `s3://bucket/key` as `image_url`. For S3 references, product reads return an HTTPS URL signed for five minutes. The catalog signs only its configured bucket. Use the original image reference from the admin replica inspection response when editing a product.

For the signed local emulator, set `FLASHKART_PRODUCT_IMAGES_BUCKET`, `FLASHKART_S3_ENDPOINT` to the loopback HTTPS endpoint, and `FLASHKART_S3_CREDENTIALS_FILE` to that emulator instance's credentials file before running `make run-market`. Use the emulator CA when fetching its HTTPS URLs. The AWS profile uses the SDK workload credential chain and standard S3 endpoints. Keep the bucket private with encryption at rest and restrict the Catalog workload role to reading the image objects. [S3 presigning examples](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/go_s3_code_examples.html)

## Verification

Run `make test-catalog-publication` after migration. The test uses the local PostgreSQL roles, three reachable replica gRPC servers, and Inventory gRPC. It checks admin access, product creation during one outage, conflict resolution, draft locking, lost replies after each publication step, stable preparation retries, and matching catalog and inventory terms.
