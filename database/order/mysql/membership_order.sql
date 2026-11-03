CREATE TABLE order_service.membership_order
(
    id                     BIGINT       NOT NULL COMMENT '会员订单内部标识，应用生成的正数雪花ID',
    order_no               VARCHAR(64)  NOT NULL COMMENT '会员订单业务编号，在本表内唯一，对外展示和查询',
    user_id                BIGINT       NOT NULL COMMENT '购买会员的用户标识，引用user_service.user_profile.id',
    plan_code              VARCHAR(64)  NOT NULL COMMENT '下单时会员套餐编码快照，不随套餐配置变化',
    plan_version           BIGINT       NOT NULL COMMENT '下单时会员套餐版本快照，用于定位购买时的套餐定义',
    membership_level       INT          NOT NULL COMMENT '下单时购买的会员等级快照，本例10表示标准会员',
    duration_seconds       BIGINT       NOT NULL COMMENT '下单时购买的会员有效时长快照，单位：秒，由应用校验为正数',
    payable_amount         BIGINT       NOT NULL COMMENT '下单时应付金额快照，单位：分，币种：CNY，本例由应用校验为正数',
    currency_code          VARCHAR(3)   NOT NULL COMMENT '下单时币种代码快照，本例固定为CNY，由应用校验',
    payment_status         INT          NOT NULL DEFAULT 10 COMMENT '支付及履约状态：10待支付，20已支付且已完成权益发放',
    payment_channel        VARCHAR(32)  NOT NULL COMMENT '下单时选定的支付渠道，本例每个渠道对应唯一固定商户账户',
    payment_transaction_no VARCHAR(128) NULL     DEFAULT NULL COMMENT '支付渠道返回的交易流水号；NULL表示尚未取得流水',
    paid_at                DATETIME(3)  NULL     DEFAULT NULL COMMENT '支付渠道确认的实际支付成功时间，UTC；NULL表示尚未确认支付',
    fulfilled_at           DATETIME(3)  NULL     DEFAULT NULL COMMENT '本地完成会员权益履约时取用的处理时间，UTC，不代表提交时间；NULL表示尚未完成履约',
    created_at             DATETIME(3)  NOT NULL COMMENT '会员订单创建时间，UTC，由应用填写',
    updated_at             DATETIME(3)  NOT NULL COMMENT '会员订单最近修改时间，UTC，由应用填写',
    PRIMARY KEY (id),
    UNIQUE KEY uk_membership_order_order_no (order_no),
    UNIQUE KEY uk_membership_order_payment_channel_payment_transaction_no
        (payment_channel, payment_transaction_no),
    KEY idx_membership_order_user_id_created_at (user_id, created_at),
    KEY idx_membership_order_payment_status_created_at (payment_status, created_at)
) ENGINE = InnoDB
  DEFAULT CHARACTER SET = utf8mb4
  COLLATE = utf8mb4_0900_bin
    COMMENT ='会员购买订单，每行保存一笔订单的套餐价格快照及当前支付履约状态';
