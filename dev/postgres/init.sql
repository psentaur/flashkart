\set ON_ERROR_STOP on
\getenv admission_password ADMISSION_DB_PASSWORD
\getenv flash_sale_password FLASH_SALE_DB_PASSWORD
\getenv order_password ORDER_DB_PASSWORD
CREATE ROLE admission LOGIN PASSWORD :'admission_password';
CREATE ROLE flash_sale LOGIN PASSWORD :'flash_sale_password';
CREATE ROLE order_service LOGIN PASSWORD :'order_password';
CREATE DATABASE flashkart_admission OWNER admission;
CREATE DATABASE flashkart_flash_sale OWNER flash_sale;
CREATE DATABASE flashkart_order OWNER order_service;
REVOKE CONNECT ON DATABASE flashkart_admission FROM PUBLIC;
REVOKE CONNECT ON DATABASE flashkart_flash_sale FROM PUBLIC;
REVOKE CONNECT ON DATABASE flashkart_order FROM PUBLIC;
GRANT CONNECT ON DATABASE flashkart_admission TO admission;
GRANT CONNECT ON DATABASE flashkart_flash_sale TO flash_sale;
GRANT CONNECT ON DATABASE flashkart_order TO order_service;
\connect flashkart_flash_sale
CREATE EXTENSION IF NOT EXISTS btree_gist;
